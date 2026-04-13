package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/sdk"
)

const (
	aiTurnKindConversation = "conversation"
	aiTurnKindInternal     = "internal"

	aiTurnRefKindMessageID = "message_id"
	aiTurnRefKindEventID   = "event_id"
)

type aiTurnRecord struct {
	TurnID           string
	Sequence         int64
	ContextEpoch     int64
	Kind             string
	Source           string
	Role             string
	SenderID         networkid.UserID
	IncludeInHistory bool
	TurnData         sdk.TurnData
	Metadata         *MessageMetadata
	MessageID        networkid.MessageID
	EventID          id.EventID
	CreatedAtMs      int64
	UpdatedAtMs      int64
}

type aiTurnUpsert struct {
	TurnID           string
	Kind             string
	Source           string
	MessageID        networkid.MessageID
	EventID          id.EventID
	SenderID         networkid.UserID
	IncludeInHistory bool
	Timestamp        time.Time
	TurnData         sdk.TurnData
	Metadata         *MessageMetadata
}

func normalizeAITurnMetadata(meta *MessageMetadata, turnData sdk.TurnData) *MessageMetadata {
	clean := cloneMessageMetadata(meta)
	if clean == nil {
		clean = &MessageMetadata{}
	}
	clean.CanonicalTurnData = turnData.ToMap()
	if clean.Role == "" {
		clean.Role = strings.TrimSpace(turnData.Role)
	}
	if clean.Body == "" {
		clean.Body = sdk.TurnText(turnData)
	}
	return clean
}

func encodeAITurnMetadata(meta *MessageMetadata) (string, error) {
	clean := cloneMessageMetadata(meta)
	if clean == nil {
		clean = &MessageMetadata{}
	}
	clean.CanonicalTurnData = nil
	raw, err := json.Marshal(clean)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func decodeAITurnMetadata(raw string, turnData sdk.TurnData) (*MessageMetadata, error) {
	if strings.TrimSpace(raw) == "" {
		return normalizeAITurnMetadata(nil, turnData), nil
	}
	var meta MessageMetadata
	if err := json.Unmarshal([]byte(raw), &meta); err != nil {
		return nil, err
	}
	return normalizeAITurnMetadata(&meta, turnData), nil
}

func allocateAITurnSequence(ctx context.Context, scope *portalScope) (contextEpoch, sequence int64, err error) {
	record, err := ensurePortalTurnStateByScope(ctx, scope)
	if err != nil {
		return 0, 0, err
	}
	contextEpoch = record.ContextEpoch
	sequence = record.NextTurnSequence + 1
	_, err = scope.db.Exec(ctx, `
		UPDATE `+aiPortalStateTable+`
		SET next_turn_sequence=$4, updated_at_ms=$5
		WHERE bridge_id=$1 AND portal_id=$2 AND portal_receiver=$3
	`, scope.bridgeID, scope.portalID, scope.portalReceiver, sequence, time.Now().UnixMilli())
	return contextEpoch, sequence, err
}

func ensurePortalTurnStateByScope(ctx context.Context, scope *portalScope) (*aiPersistedPortalRecord, error) {
	if scope == nil {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	nowMs := time.Now().UnixMilli()
	if _, err := scope.db.Exec(ctx, `
		INSERT INTO `+aiPortalStateTable+` (
			bridge_id, portal_id, portal_receiver, state_json, context_epoch, next_turn_sequence, updated_at_ms
		) VALUES ($1, $2, $3, '{}', 0, 0, $4)
		ON CONFLICT (bridge_id, portal_id, portal_receiver) DO NOTHING
	`, scope.bridgeID, scope.portalID, scope.portalReceiver, nowMs); err != nil {
		return nil, err
	}
	return loadAIPortalRecordByScope(ctx, scope)
}

func loadAITurnByRef(ctx context.Context, portal *bridgev2.Portal, messageID networkid.MessageID, eventID id.EventID) (*aiTurnRecord, error) {
	scope, err := portalScopeForAIDB(ctx, portal)
	if err != nil {
		return nil, err
	}
	if scope == nil {
		return nil, nil
	}
	if row, err := loadAITurnByRefValue(ctx, scope, aiTurnRefKindMessageID, strings.TrimSpace(string(messageID))); err != nil || row != nil {
		return row, err
	}
	return loadAITurnByRefValue(ctx, scope, aiTurnRefKindEventID, strings.TrimSpace(eventID.String()))
}

func loadAITurnByRefValue(ctx context.Context, scope *portalScope, refKind, refValue string) (*aiTurnRecord, error) {
	if scope == nil || refKind == "" || strings.TrimSpace(refValue) == "" {
		return nil, nil
	}
	rows, err := queryAITurnRows(ctx, scope, aiTurnQuery{
		refKind:  refKind,
		refValue: refValue,
		limit:    1,
	})
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return rows[0], nil
}

func upsertAITurn(ctx context.Context, portal *bridgev2.Portal, entry aiTurnUpsert) error {
	scope, err := portalScopeForAIDB(ctx, portal)
	if err != nil {
		return err
	}
	if scope == nil {
		return fmt.Errorf("ai turn scope unavailable for portal %s", portal.PortalKey)
	}
	role := strings.TrimSpace(entry.TurnData.Role)
	if role == "" && entry.Metadata != nil {
		role = strings.TrimSpace(entry.Metadata.Role)
	}
	if role == "" {
		return nil
	}
	entry.TurnData.Role = role
	if strings.TrimSpace(entry.TurnID) != "" {
		entry.TurnData.ID = strings.TrimSpace(entry.TurnID)
	}
	return scope.db.DoTxn(ctx, nil, func(ctx context.Context) error {
		existing, err := resolveExistingAITurnForUpdate(ctx, scope, entry)
		if err != nil {
			return err
		}

		turnID := strings.TrimSpace(entry.TurnData.ID)
		contextEpoch := int64(0)
		sequence := int64(0)
		createdAtMs := entry.Timestamp.UnixMilli()
		if entry.Timestamp.IsZero() {
			createdAtMs = time.Now().UnixMilli()
		}
		if existing != nil {
			turnID = existing.TurnID
			contextEpoch = existing.ContextEpoch
			sequence = existing.Sequence
			if existing.CreatedAtMs > 0 {
				createdAtMs = existing.CreatedAtMs
			}
		} else {
			if turnID == "" {
				turnID = sdk.NewTurnID()
			}
			contextEpoch, sequence, err = allocateAITurnSequence(ctx, scope)
			if err != nil {
				return err
			}
		}
		entry.TurnData.ID = turnID
		meta := normalizeAITurnMetadata(entry.Metadata, entry.TurnData)
		metaJSON, err := encodeAITurnMetadata(meta)
		if err != nil {
			return err
		}
		turnJSON, err := json.Marshal(entry.TurnData.ToMap())
		if err != nil {
			return err
		}
		nowMs := time.Now().UnixMilli()
		if _, err = scope.db.Exec(ctx, `
			INSERT INTO `+aiTurnsTable+` (
				bridge_id, portal_id, portal_receiver, turn_id, context_epoch, sequence, kind, source, role,
				sender_id, include_in_history, turn_data_json, meta_json, created_at_ms, updated_at_ms
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
			ON CONFLICT (bridge_id, portal_id, portal_receiver, turn_id) DO UPDATE SET
				kind=excluded.kind,
				source=excluded.source,
				role=excluded.role,
				sender_id=excluded.sender_id,
				include_in_history=excluded.include_in_history,
				turn_data_json=excluded.turn_data_json,
				meta_json=excluded.meta_json,
				updated_at_ms=excluded.updated_at_ms
		`, scope.bridgeID, scope.portalID, scope.portalReceiver, turnID, contextEpoch, sequence,
			normalizeAITurnKind(entry.Kind), strings.TrimSpace(entry.Source), role, string(entry.SenderID),
			entry.IncludeInHistory, string(turnJSON), metaJSON, createdAtMs, nowMs); err != nil {
			return err
		}
		if err := replaceAITurnRef(ctx, scope, turnID, aiTurnRefKindMessageID, strings.TrimSpace(string(entry.MessageID))); err != nil {
			return err
		}
		if err := replaceAITurnRef(ctx, scope, turnID, aiTurnRefKindEventID, strings.TrimSpace(entry.EventID.String())); err != nil {
			return err
		}
		return nil
	})
}

func normalizeAITurnKind(kind string) string {
	switch strings.TrimSpace(kind) {
	case aiTurnKindInternal:
		return aiTurnKindInternal
	default:
		return aiTurnKindConversation
	}
}

func resolveExistingAITurnForUpdate(ctx context.Context, scope *portalScope, entry aiTurnUpsert) (*aiTurnRecord, error) {
	if row, err := loadAITurnByRefValue(ctx, scope, aiTurnRefKindMessageID, strings.TrimSpace(string(entry.MessageID))); err != nil || row != nil {
		return row, err
	}
	if row, err := loadAITurnByRefValue(ctx, scope, aiTurnRefKindEventID, strings.TrimSpace(entry.EventID.String())); err != nil || row != nil {
		return row, err
	}
	if strings.TrimSpace(entry.TurnID) == "" {
		return nil, nil
	}
	rows, err := queryAITurnRows(ctx, scope, aiTurnQuery{
		turnID: entry.TurnID,
		limit:  1,
	})
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return rows[0], nil
}

func replaceAITurnRef(ctx context.Context, scope *portalScope, turnID, refKind, refValue string) error {
	if scope == nil || turnID == "" || refKind == "" {
		return nil
	}
	if _, err := scope.db.Exec(ctx, `
		DELETE FROM `+aiTurnRefsTable+`
		WHERE bridge_id=$1 AND portal_id=$2 AND portal_receiver=$3 AND turn_id=$4 AND ref_kind=$5
	`, scope.bridgeID, scope.portalID, scope.portalReceiver, turnID, refKind); err != nil {
		return err
	}
	if strings.TrimSpace(refValue) == "" {
		return nil
	}
	_, err := scope.db.Exec(ctx, `
		INSERT INTO `+aiTurnRefsTable+` (
			bridge_id, portal_id, portal_receiver, ref_kind, ref_value, turn_id
		) VALUES ($1, $2, $3, $4, $5, $6)
	`, scope.bridgeID, scope.portalID, scope.portalReceiver, refKind, refValue, turnID)
	return err
}

func deleteAITurnByExternalRef(ctx context.Context, portal *bridgev2.Portal, messageID networkid.MessageID, eventID id.EventID) error {
	scope, err := portalScopeForAIDB(ctx, portal)
	if err != nil {
		return err
	}
	if scope == nil {
		return nil
	}
	record, err := loadAITurnByRef(ctx, portal, messageID, eventID)
	if err != nil || record == nil {
		return err
	}
	return scope.db.DoTxn(ctx, nil, func(ctx context.Context) error {
		if _, err := scope.db.Exec(ctx, `
			DELETE FROM `+aiTurnRefsTable+`
			WHERE bridge_id=$1 AND portal_id=$2 AND portal_receiver=$3 AND turn_id=$4
		`, scope.bridgeID, scope.portalID, scope.portalReceiver, record.TurnID); err != nil {
			return err
		}
		_, err := scope.db.Exec(ctx, `
			DELETE FROM `+aiTurnsTable+`
			WHERE bridge_id=$1 AND portal_id=$2 AND portal_receiver=$3 AND turn_id=$4
		`, scope.bridgeID, scope.portalID, scope.portalReceiver, record.TurnID)
		return err
	})
}

func deleteAITurnsForPortal(ctx context.Context, portal *bridgev2.Portal) {
	scope, err := portalScopeForAIDB(ctx, portal)
	if err != nil || scope == nil {
		return
	}
	log := portal.Bridge.Log
	execDelete(ctx, scope.db, &log,
		`DELETE FROM `+aiTurnRefsTable+` WHERE bridge_id=$1 AND portal_id=$2 AND portal_receiver=$3`,
		scope.bridgeID, scope.portalID, scope.portalReceiver,
	)
	execDelete(ctx, scope.db, &log,
		`DELETE FROM `+aiTurnsTable+` WHERE bridge_id=$1 AND portal_id=$2 AND portal_receiver=$3`,
		scope.bridgeID, scope.portalID, scope.portalReceiver,
	)
}

func persistAIConversationMessage(ctx context.Context, portal *bridgev2.Portal, msg *database.Message) error {
	if msg == nil {
		return nil
	}
	meta, ok := msg.Metadata.(*MessageMetadata)
	if !ok || meta == nil {
		return nil
	}
	turnData, ok := canonicalTurnData(meta)
	if !ok {
		return nil
	}
	return upsertAITurn(ctx, portal, aiTurnUpsert{
		TurnID:           strings.TrimSpace(turnData.ID),
		Kind:             aiTurnKindConversation,
		MessageID:        msg.ID,
		EventID:          msg.MXID,
		SenderID:         msg.SenderID,
		IncludeInHistory: !meta.ExcludeFromHistory,
		Timestamp:        msg.Timestamp,
		TurnData:         turnData,
		Metadata:         meta,
	})
}

func persistAIInternalPromptTurn(
	ctx context.Context,
	portal *bridgev2.Portal,
	eventID id.EventID,
	promptContext PromptContext,
	excludeFromHistory bool,
	source string,
	timestamp time.Time,
) error {
	if portal == nil || eventID == "" {
		return nil
	}
	meta := &MessageMetadata{}
	setCanonicalTurnDataFromPromptMessages(meta, promptTail(promptContext, 1))
	turnData, ok := canonicalTurnData(meta)
	if !ok {
		return nil
	}
	return upsertAITurn(ctx, portal, aiTurnUpsert{
		TurnID:           strings.TrimSpace(turnData.ID),
		Kind:             aiTurnKindInternal,
		Source:           source,
		MessageID:        sdk.MatrixMessageID(eventID),
		EventID:          eventID,
		SenderID:         humanUserID(networkid.UserLoginID(portal.PortalKey.Receiver)),
		IncludeInHistory: !excludeFromHistory,
		Timestamp:        timestamp,
		TurnData:         turnData,
		Metadata:         meta,
	})
}

func loadAIConversationMessage(ctx context.Context, portal *bridgev2.Portal, messageID networkid.MessageID, eventID id.EventID) (*database.Message, error) {
	record, err := loadAITurnByRef(ctx, portal, messageID, eventID)
	if err != nil || record == nil {
		return nil, err
	}
	if record.Kind != aiTurnKindConversation {
		return nil, nil
	}
	return databaseMessageFromAITurn(portal, record), nil
}

func databaseMessageFromAITurn(portal *bridgev2.Portal, record *aiTurnRecord) *database.Message {
	if record == nil {
		return nil
	}
	msg := &database.Message{
		ID:        record.MessageID,
		MXID:      record.EventID,
		SenderID:  record.SenderID,
		Timestamp: time.UnixMilli(record.CreatedAtMs),
		Metadata:  normalizeAITurnMetadata(record.Metadata, record.TurnData),
	}
	if msg.ID == "" {
		msg.ID = networkid.MessageID(record.TurnID)
	}
	if portal != nil {
		msg.Room = portal.PortalKey
	}
	return msg
}

func loadAIPromptHistoryTurns(
	ctx context.Context,
	portal *bridgev2.Portal,
	limit int,
	opts historyReplayOptions,
) ([]*aiTurnRecord, error) {
	scope, err := portalScopeForAIDB(ctx, portal)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		return nil, nil
	}
	if scope == nil {
		return nil, fmt.Errorf("ai history scope unavailable for portal %s", portal.PortalKey)
	}
	record, err := ensurePortalTurnStateByScope(ctx, scope)
	if err != nil || record == nil {
		return nil, err
	}
	query := aiTurnQuery{
		contextEpoch:     record.ContextEpoch,
		hasContextEpoch:  true,
		includeInHistory: true,
		limit:            limit,
	}
	if opts.targetMessageID != "" {
		target, err := loadAITurnByRef(ctx, portal, opts.targetMessageID, "")
		if err != nil {
			return nil, err
		}
		if target != nil {
			query.maxSequenceExclusive = target.Sequence
			query.contextEpoch = target.ContextEpoch
			query.hasContextEpoch = true
		}
	}
	return queryAITurnRows(ctx, scope, query)
}

func hasInternalPromptHistory(ctx context.Context, portal *bridgev2.Portal) bool {
	scope, err := portalScopeForAIDB(ctx, portal)
	if err != nil {
		return false
	}
	if scope == nil {
		return false
	}
	record, err := ensurePortalTurnStateByScope(ctx, scope)
	if err != nil || record == nil {
		return false
	}
	var count int
	err = scope.db.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM `+aiTurnsTable+`
		WHERE bridge_id=$1 AND portal_id=$2 AND portal_receiver=$3
		  AND context_epoch=$4
		  AND kind=$5
		  AND include_in_history=1
	`, scope.bridgeID, scope.portalID, scope.portalReceiver, record.ContextEpoch, aiTurnKindInternal).Scan(&count)
	return err == nil && count > 0
}

func (oc *AIClient) getAIHistoryMessages(ctx context.Context, portal *bridgev2.Portal, limit int) ([]*database.Message, error) {
	if oc == nil || portal == nil || portal.MXID == "" || limit <= 0 {
		return nil, nil
	}
	log := oc.loggerForContext(ctx).With().
		Str("portal_key_id", string(portal.PortalKey.ID)).
		Str("portal_key_receiver", string(portal.PortalKey.Receiver)).
		Str("portal_mxid", portal.MXID.String()).
		Int("history_limit", limit).
		Logger()
	scope, err := portalScopeForAIDB(ctx, portal)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to resolve canonical portal for AI history load")
		return nil, err
	}
	if scope == nil {
		err = fmt.Errorf("ai history scope unavailable for portal %s", portal.PortalKey)
		log.Warn().
			Err(err).
			Str("portal_bridge_id", string(portal.BridgeID)).
			Msg("Canonical AI history scope is unavailable")
		return nil, err
	}
	record, err := ensurePortalTurnStateByScope(ctx, scope)
	if err != nil || record == nil {
		return nil, err
	}
	rows, err := queryAITurnRows(ctx, scope, aiTurnQuery{
		contextEpoch:     record.ContextEpoch,
		hasContextEpoch:  true,
		includeInHistory: true,
		kind:             aiTurnKindConversation,
		roles:            []string{"user", "assistant"},
		limit:            limit,
	})
	if err != nil {
		log.Warn().
			Err(err).
			Str("bridge_id", scope.bridgeID).
			Str("portal_id", scope.portalID).
			Str("portal_receiver", scope.portalReceiver).
			Msg("Failed to load AI turn history")
		return nil, err
	}
	messages := make([]*database.Message, 0, len(rows))
	for _, row := range rows {
		messages = append(messages, databaseMessageFromAITurn(portal, row))
	}
	log.Debug().
		Str("bridge_id", scope.bridgeID).
		Str("portal_id", scope.portalID).
		Str("portal_receiver", scope.portalReceiver).
		Int("history_count", len(messages)).
		Str("history_sample", transcriptHistorySummary(messages, 3)).
		Msg("Loaded AI turn history")
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
		sqlQuery += ` AND t.include_in_history=1`
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
