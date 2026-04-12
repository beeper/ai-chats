package ai

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/sdk"
)

type internalPromptHistoryRecord struct {
	MessageID networkid.MessageID
	Role      string
	Messages  []PromptMessage
	CreatedAt int64
}

func persistInternalPrompt(
	ctx context.Context,
	portal *bridgev2.Portal,
	eventID id.EventID,
	promptContext PromptContext,
	excludeFromHistory bool,
	source string,
	timestamp time.Time,
) error {
	scope := portalScopeForPortal(portal)
	if scope == nil || eventID == "" {
		return nil
	}
	meta := &MessageMetadata{}
	setCanonicalTurnDataFromPromptMessages(meta, promptTail(promptContext, 1))
	if len(meta.CanonicalTurnData) == 0 {
		return nil
	}
	rawTurnData, err := json.Marshal(meta.CanonicalTurnData)
	if err != nil {
		return err
	}
	if timestamp.IsZero() {
		timestamp = time.Now()
	}
	_, err = scope.db.Exec(ctx, `
		INSERT INTO `+aiInternalMessagesTable+` (
			bridge_id, login_id, portal_id, event_id, source, canonical_turn_data, exclude_from_history, created_at_ms
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (bridge_id, login_id, portal_id, event_id) DO UPDATE SET
			source=excluded.source,
			canonical_turn_data=excluded.canonical_turn_data,
			exclude_from_history=excluded.exclude_from_history,
			created_at_ms=excluded.created_at_ms
	`,
		scope.bridgeID,
		scope.loginID,
		scope.portalID,
		eventID.String(),
		strings.TrimSpace(source),
		string(rawTurnData),
		excludeFromHistory,
		timestamp.UnixMilli(),
	)
	return err
}

func loadInternalPromptHistory(
	ctx context.Context,
	portal *bridgev2.Portal,
	limit int,
	opts historyReplayOptions,
	resetAt int64,
) ([]internalPromptHistoryRecord, error) {
	scope := portalScopeForPortal(portal)
	if scope == nil || limit <= 0 {
		return nil, nil
	}
	rows, err := scope.db.Query(ctx, `
		SELECT event_id, canonical_turn_data, exclude_from_history, created_at_ms
		FROM `+aiInternalMessagesTable+`
		WHERE bridge_id=$1 AND login_id=$2 AND portal_id=$3
		ORDER BY created_at_ms DESC, event_id DESC
		LIMIT $4
	`, scope.bridgeID, scope.loginID, scope.portalID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []internalPromptHistoryRecord
	for rows.Next() {
		var (
			eventID            string
			rawTurnData        string
			excludeFromHistory bool
			createdAtMs        int64
		)
		if err = rows.Scan(&eventID, &rawTurnData, &excludeFromHistory, &createdAtMs); err != nil {
			return nil, err
		}
		if excludeFromHistory {
			continue
		}
		messageID := sdk.MatrixMessageID(id.EventID(eventID))
		if opts.excludeMessageID != "" && messageID == opts.excludeMessageID {
			continue
		}
		if resetAt > 0 && createdAtMs < resetAt {
			continue
		}
		var raw map[string]any
		if err = json.Unmarshal([]byte(rawTurnData), &raw); err != nil {
			return nil, err
		}
		turnData, ok := sdk.DecodeTurnData(raw)
		if !ok {
			continue
		}
		messages := filterPromptMessagesForHistory(promptMessagesFromTurnData(turnData), false)
		if len(messages) == 0 {
			continue
		}
		out = append(out, internalPromptHistoryRecord{
			MessageID: messageID,
			Role:      strings.TrimSpace(turnData.Role),
			Messages:  messages,
			CreatedAt: createdAtMs,
		})
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func hasInternalPromptHistory(ctx context.Context, portal *bridgev2.Portal) bool {
	scope := portalScopeForPortal(portal)
	if scope == nil {
		return false
	}
	var count int
	err := scope.db.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM `+aiInternalMessagesTable+`
		WHERE bridge_id=$1 AND login_id=$2 AND portal_id=$3 AND exclude_from_history=0
	`, scope.bridgeID, scope.loginID, scope.portalID).Scan(&count)
	return err == nil && count > 0
}

func deleteInternalPromptsForPortal(ctx context.Context, portal *bridgev2.Portal) {
	scope := portalScopeForPortal(portal)
	if scope == nil {
		return
	}
	log := portal.Bridge.Log
	execDelete(ctx, scope.db, &log,
		`DELETE FROM `+aiInternalMessagesTable+` WHERE bridge_id=$1 AND login_id=$2 AND portal_id=$3`,
		scope.bridgeID, scope.loginID, scope.portalID,
	)
}
