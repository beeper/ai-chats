package ai

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/sdk"
)

type internalPromptDBScope struct {
	db       *dbutil.Database
	bridgeID string
	loginID  string
}

type internalPromptHistoryRecord struct {
	MessageID networkid.MessageID
	Role      string
	Messages  []PromptMessage
	CreatedAt int64
}

func internalPromptScope(client *AIClient) *internalPromptDBScope {
	db, bridgeID, loginID := loginDBContext(client)
	if db == nil || strings.TrimSpace(bridgeID) == "" || strings.TrimSpace(loginID) == "" {
		return nil
	}
	return &internalPromptDBScope{
		db:       db,
		bridgeID: bridgeID,
		loginID:  loginID,
	}
}

func persistInternalPrompt(
	ctx context.Context,
	client *AIClient,
	portal *bridgev2.Portal,
	eventID id.EventID,
	promptContext PromptContext,
	excludeFromHistory bool,
	source string,
	timestamp time.Time,
) error {
	scope := internalPromptScope(client)
	if scope == nil || portal == nil || portal.MXID == "" || eventID == "" {
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
			bridge_id, login_id, room_id, event_id, source, canonical_turn_data, exclude_from_history, created_at_ms
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (bridge_id, login_id, room_id, event_id) DO UPDATE SET
			source=excluded.source,
			canonical_turn_data=excluded.canonical_turn_data,
			exclude_from_history=excluded.exclude_from_history,
			created_at_ms=excluded.created_at_ms
	`,
		scope.bridgeID,
		scope.loginID,
		portal.MXID.String(),
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
	client *AIClient,
	portal *bridgev2.Portal,
	limit int,
	opts historyReplayOptions,
	resetAt int64,
) ([]internalPromptHistoryRecord, error) {
	scope := internalPromptScope(client)
	if scope == nil || portal == nil || portal.MXID == "" || limit <= 0 {
		return nil, nil
	}
	rows, err := scope.db.Query(ctx, `
		SELECT event_id, canonical_turn_data, exclude_from_history, created_at_ms
		FROM `+aiInternalMessagesTable+`
		WHERE bridge_id=$1 AND login_id=$2 AND room_id=$3
		ORDER BY created_at_ms DESC, event_id DESC
		LIMIT $4
	`, scope.bridgeID, scope.loginID, portal.MXID.String(), limit)
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

func hasInternalPromptHistory(ctx context.Context, client *AIClient, roomID id.RoomID) bool {
	scope := internalPromptScope(client)
	if scope == nil || roomID == "" {
		return false
	}
	var count int
	err := scope.db.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM `+aiInternalMessagesTable+`
		WHERE bridge_id=$1 AND login_id=$2 AND room_id=$3 AND exclude_from_history=0
	`, scope.bridgeID, scope.loginID, roomID.String()).Scan(&count)
	return err == nil && count > 0
}

func deleteInternalPromptsForRoom(ctx context.Context, client *AIClient, roomID id.RoomID) {
	scope := internalPromptScope(client)
	if scope == nil || roomID == "" {
		return
	}
	execDelete(ctx, scope.db, client.Log(),
		`DELETE FROM `+aiInternalMessagesTable+` WHERE bridge_id=$1 AND login_id=$2 AND room_id=$3`,
		scope.bridgeID, scope.loginID, roomID.String(),
	)
}
