package ai

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"
)

func cloneCanonicalTurnData(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	data, err := json.Marshal(src)
	if err != nil {
		return nil
	}
	var clone map[string]any
	if err = json.Unmarshal(data, &clone); err != nil {
		return nil
	}
	return clone
}

func cloneMessageMetadata(src *MessageMetadata) *MessageMetadata {
	if src == nil {
		return nil
	}
	data, err := json.Marshal(src)
	if err != nil {
		clone := &MessageMetadata{}
		clone.CopyFrom(src)
		clone.MediaUnderstanding = append([]MediaUnderstandingOutput(nil), src.MediaUnderstanding...)
		clone.MediaUnderstandingDecisions = append([]MediaUnderstandingDecision(nil), src.MediaUnderstandingDecisions...)
		clone.MediaURL = src.MediaURL
		clone.MimeType = src.MimeType
		return clone
	}
	var clone MessageMetadata
	if err = json.Unmarshal(data, &clone); err != nil {
		fallback := &MessageMetadata{}
		fallback.CopyFrom(src)
		fallback.MediaUnderstanding = append([]MediaUnderstandingOutput(nil), src.MediaUnderstanding...)
		fallback.MediaUnderstandingDecisions = append([]MediaUnderstandingDecision(nil), src.MediaUnderstandingDecisions...)
		fallback.MediaURL = src.MediaURL
		fallback.MimeType = src.MimeType
		return fallback
	}
	return &clone
}

func cloneMessageForAIHistory(msg *database.Message) *database.Message {
	if msg == nil {
		return nil
	}
	clone := *msg
	if meta, ok := msg.Metadata.(*MessageMetadata); ok {
		clone.Metadata = cloneMessageMetadata(meta)
	}
	return &clone
}

func persistAITranscriptMessage(ctx context.Context, client *AIClient, portal *bridgev2.Portal, msg *database.Message) error {
	scope := portalScopeForPortal(portal)
	if scope == nil || client == nil || msg == nil || strings.TrimSpace(string(msg.ID)) == "" {
		return nil
	}
	meta, ok := msg.Metadata.(*MessageMetadata)
	if !ok || meta == nil {
		return nil
	}
	payload, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	createdAt := msg.Timestamp.UnixMilli()
	if createdAt == 0 {
		createdAt = time.Now().UnixMilli()
	}
	_, err = scope.db.Exec(ctx, `
		INSERT INTO `+aiTranscriptTable+` (
			bridge_id, login_id, portal_id, message_id, event_id, sender_id, metadata_json, created_at_ms, updated_at_ms
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (bridge_id, login_id, portal_id, message_id) DO UPDATE SET
			event_id=excluded.event_id,
			sender_id=excluded.sender_id,
			metadata_json=excluded.metadata_json,
			created_at_ms=excluded.created_at_ms,
			updated_at_ms=excluded.updated_at_ms
	`,
		scope.bridgeID,
		scope.loginID,
		scope.portalID,
		string(msg.ID),
		msg.MXID.String(),
		string(msg.SenderID),
		string(payload),
		createdAt,
		time.Now().UnixMilli(),
	)
	return err
}

func loadAITranscriptMessage(ctx context.Context, portal *bridgev2.Portal, messageID networkid.MessageID) (*database.Message, error) {
	messages, err := loadAITranscriptMessages(ctx, portal, []networkid.MessageID{messageID}, 1)
	if err != nil || len(messages) == 0 {
		return nil, err
	}
	return messages[0], nil
}

func loadAITranscriptMessages(
	ctx context.Context,
	portal *bridgev2.Portal,
	messageIDs []networkid.MessageID,
	limit int,
) ([]*database.Message, error) {
	scope := portalScopeForPortal(portal)
	if scope == nil {
		return nil, nil
	}
	args := []any{scope.bridgeID, scope.loginID, scope.portalID}
	query := `
		SELECT message_id, event_id, sender_id, metadata_json, created_at_ms
		FROM ` + aiTranscriptTable + `
		WHERE bridge_id=$1 AND login_id=$2 AND portal_id=$3
	`
	if len(messageIDs) > 0 {
		placeholders := make([]string, 0, len(messageIDs))
		for _, messageID := range messageIDs {
			if strings.TrimSpace(string(messageID)) == "" {
				continue
			}
			args = append(args, string(messageID))
			placeholders = append(placeholders, "$"+strconv.Itoa(len(args)))
		}
		if len(placeholders) == 0 {
			return nil, nil
		}
		query += ` AND message_id IN (` + strings.Join(placeholders, ", ") + `)`
	}
	query += ` ORDER BY created_at_ms DESC, message_id DESC`
	if limit > 0 {
		args = append(args, limit)
		query += ` LIMIT $` + strconv.Itoa(len(args))
	}
	rows, err := scope.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*database.Message
	for rows.Next() {
		var (
			messageID   string
			eventID     string
			senderID    string
			metadataRaw string
			createdAtMs int64
		)
		if err = rows.Scan(&messageID, &eventID, &senderID, &metadataRaw, &createdAtMs); err != nil {
			return nil, err
		}
		if strings.TrimSpace(messageID) == "" || strings.TrimSpace(metadataRaw) == "" {
			continue
		}
		var meta MessageMetadata
		if err = json.Unmarshal([]byte(metadataRaw), &meta); err != nil {
			return nil, err
		}
		out = append(out, &database.Message{
			ID:        networkid.MessageID(messageID),
			MXID:      id.EventID(eventID),
			SenderID:  networkid.UserID(senderID),
			Metadata:  &meta,
			Timestamp: time.UnixMilli(createdAtMs),
		})
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (oc *AIClient) getAIHistoryMessages(ctx context.Context, portal *bridgev2.Portal, limit int) ([]*database.Message, error) {
	if oc == nil || portal == nil || portal.MXID == "" {
		return nil, nil
	}
	messages, err := loadAITranscriptMessages(ctx, portal, nil, limit)
	if err != nil {
		return nil, err
	}
	for _, msg := range messages {
		if msg != nil {
			msg.Room = portal.PortalKey
		}
	}
	return messages, nil
}

func (oc *AIClient) getAllAIHistoryMessages(ctx context.Context, portal *bridgev2.Portal) ([]*database.Message, error) {
	if oc == nil || portal == nil || portal.MXID == "" {
		return nil, nil
	}
	return oc.getAIHistoryMessages(ctx, portal, 0)
}
