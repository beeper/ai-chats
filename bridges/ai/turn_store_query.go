package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/sdk"
)

func loadAICurrentContextTurnsByScope(ctx context.Context, scope *portalScope, query aiTurnQuery) ([]*aiTurnRecord, error) {
	if scope == nil || query.limit <= 0 {
		return nil, nil
	}
	record, err := ensureAIPortalRecordByScope(ctx, scope)
	if err != nil || record == nil {
		return nil, err
	}
	if !query.hasContextEpoch {
		query.contextEpoch = record.ContextEpoch
		query.hasContextEpoch = true
	}
	return queryAITurnRows(ctx, scope, query)
}

func (oc *AIClient) loadAIHistoryMessagesFromTurns(ctx context.Context, portal *bridgev2.Portal, limit int) ([]*database.Message, error) {
	if oc == nil || portal == nil || portal.MXID == "" || limit <= 0 {
		return nil, nil
	}
	return withResolvedPortalScopeValue(ctx, oc, portal, func(ctx context.Context, portal *bridgev2.Portal, scope *portalScope) ([]*database.Message, error) {
		rows, err := loadAICurrentContextTurnsByScope(ctx, scope, aiTurnQuery{
			includeInHistory: true,
			roles:            []string{"user", "assistant"},
			limit:            limit,
		})
		if err != nil {
			return nil, err
		}
		messages := make([]*database.Message, 0, len(rows))
		for _, row := range rows {
			msg := aiHistoryMessageFromTurn(portal.PortalKey, row)
			if msg == nil {
				continue
			}
			msgMeta := messageMeta(msg)
			if !shouldIncludeInHistory(msgMeta) {
				continue
			}
			messages = append(messages, msg)
		}
		return messages, nil
	})
}

func (oc *AIClient) getAIHistoryMessages(ctx context.Context, portal *bridgev2.Portal, limit int) ([]*database.Message, error) {
	if oc == nil || portal == nil || portal.MXID == "" || limit <= 0 {
		return nil, nil
	}
	rows, err := oc.loadAIHistoryMessagesFromTurns(ctx, portal, limit)
	if err != nil {
		return nil, err
	}
	messages := make([]*database.Message, 0, len(rows))
	for _, msg := range rows {
		messages = append(messages, cloneMessageForAIHistory(msg))
	}
	return messages, nil
}

type aiTurnQuery struct {
	contextEpoch         int64
	hasContextEpoch      bool
	includeInHistory     bool
	kind                 string
	roles                []string
	refKind              string
	refValue             string
	turnID               string
	maxSequenceExclusive int64
	limit                int
}

func queryAITurnRows(ctx context.Context, scope *portalScope, query aiTurnQuery) ([]*aiTurnRecord, error) {
	if scope == nil {
		return nil, nil
	}
	args := []any{scope.bridgeID, scope.portalID, scope.portalReceiver}
	sqlQuery := `
		SELECT
			t.turn_id,
			t.sequence,
			t.context_epoch,
			t.kind,
			t.source,
			t.role,
			t.sender_id,
			t.include_in_history,
			t.turn_data_json,
			t.meta_json,
			t.created_at_ms,
			t.updated_at_ms,
			COALESCE(MAX(CASE WHEN r.ref_kind='message_id' THEN r.ref_value END), ''),
			COALESCE(MAX(CASE WHEN r.ref_kind='event_id' THEN r.ref_value END), '')
		FROM ` + aiTurnsTable + ` t
		LEFT JOIN ` + aiTurnRefsTable + ` r
			ON r.bridge_id=t.bridge_id
		   AND r.portal_id=t.portal_id
		   AND r.portal_receiver=t.portal_receiver
		   AND r.turn_id=t.turn_id
		WHERE t.bridge_id=$1 AND t.portal_id=$2 AND t.portal_receiver=$3
	`
	if query.turnID != "" {
		args = append(args, query.turnID)
		sqlQuery += ` AND t.turn_id=$` + strconv.Itoa(len(args))
	}
	if query.hasContextEpoch {
		args = append(args, query.contextEpoch)
		sqlQuery += ` AND t.context_epoch=$` + strconv.Itoa(len(args))
	}
	if query.kind != "" {
		args = append(args, query.kind)
		sqlQuery += ` AND t.kind=$` + strconv.Itoa(len(args))
	}
	if query.includeInHistory {
		sqlQuery += ` AND t.include_in_history=true`
	}
	if query.maxSequenceExclusive > 0 {
		args = append(args, query.maxSequenceExclusive)
		sqlQuery += ` AND t.sequence < $` + strconv.Itoa(len(args))
	}
	if query.refKind != "" && query.refValue != "" {
		args = append(args, query.refKind, query.refValue)
		sqlQuery += ` AND EXISTS (
			SELECT 1 FROM ` + aiTurnRefsTable + ` ref
			WHERE ref.bridge_id=t.bridge_id
			  AND ref.portal_id=t.portal_id
			  AND ref.portal_receiver=t.portal_receiver
			  AND ref.turn_id=t.turn_id
			  AND ref.ref_kind=$` + strconv.Itoa(len(args)-1) + `
			  AND ref.ref_value=$` + strconv.Itoa(len(args)) + `
		)`
	}
	if len(query.roles) > 0 {
		placeholders := make([]string, 0, len(query.roles))
		for _, role := range query.roles {
			if strings.TrimSpace(role) == "" {
				continue
			}
			args = append(args, role)
			placeholders = append(placeholders, "$"+strconv.Itoa(len(args)))
		}
		if len(placeholders) > 0 {
			sqlQuery += ` AND t.role IN (` + strings.Join(placeholders, ", ") + `)`
		}
	}
	sqlQuery += `
		GROUP BY
			t.turn_id, t.sequence, t.context_epoch, t.kind, t.source, t.role, t.sender_id,
			t.include_in_history, t.turn_data_json, t.meta_json, t.created_at_ms, t.updated_at_ms
		ORDER BY t.sequence DESC, t.turn_id DESC
	`
	if query.limit > 0 {
		args = append(args, query.limit)
		sqlQuery += ` LIMIT $` + strconv.Itoa(len(args))
	}
	rows, err := scope.db.Query(ctx, sqlQuery, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*aiTurnRecord
	for rows.Next() {
		var (
			row              aiTurnRecord
			senderID         string
			includeInHistory bool
			turnJSON         string
			metaJSON         string
			messageID        string
			eventID          string
		)
		if err := rows.Scan(
			&row.TurnID,
			&row.Sequence,
			&row.ContextEpoch,
			&row.Kind,
			&row.Source,
			&row.Role,
			&senderID,
			&includeInHistory,
			&turnJSON,
			&metaJSON,
			&row.CreatedAtMs,
			&row.UpdatedAtMs,
			&messageID,
			&eventID,
		); err != nil {
			return nil, err
		}
		row.SenderID = networkid.UserID(senderID)
		row.IncludeInHistory = includeInHistory
		row.MessageID = networkid.MessageID(strings.TrimSpace(messageID))
		row.EventID = id.EventID(strings.TrimSpace(eventID))

		var raw map[string]any
		if err := json.Unmarshal([]byte(turnJSON), &raw); err != nil {
			return nil, err
		}
		turnData, ok := sdk.DecodeTurnData(raw)
		if !ok {
			return nil, fmt.Errorf("invalid stored turn data for %s", row.TurnID)
		}
		row.TurnData = turnData
		meta, err := decodeAITurnMetadata(metaJSON, turnData)
		if err != nil {
			return nil, err
		}
		row.Metadata = meta
		out = append(out, &row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
