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
)

func transcriptMetaSummary(meta *MessageMetadata) string {
	if meta == nil {
		return "meta=nil"
	}
	bodyLen := len(strings.TrimSpace(meta.Body))
	return fmt.Sprintf(
		"role=%q body_len=%d canonical_keys=%d exclude=%t media_url=%t mime=%q",
		meta.Role,
		bodyLen,
		len(meta.CanonicalTurnData),
		meta.ExcludeFromHistory,
		strings.TrimSpace(meta.MediaURL) != "",
		strings.TrimSpace(meta.MimeType),
	)
}

func transcriptHistorySummary(messages []*database.Message, maxItems int) string {
	if len(messages) == 0 {
		return "empty"
	}
	if maxItems <= 0 {
		maxItems = 1
	}
	if maxItems > len(messages) {
		maxItems = len(messages)
	}
	parts := make([]string, 0, maxItems)
	for i := 0; i < maxItems; i++ {
		msg := messages[i]
		if msg == nil {
			parts = append(parts, "<nil>")
			continue
		}
		meta, _ := msg.Metadata.(*MessageMetadata)
		parts = append(parts, fmt.Sprintf(
			"id=%q event=%q %s",
			msg.ID,
			msg.MXID,
			transcriptMetaSummary(meta),
		))
	}
	return strings.Join(parts, " | ")
}

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
	if client == nil || msg == nil {
		return nil
	}
	log := client.loggerForContext(ctx)
	if scope == nil {
		portalKeyID := ""
		portalKeyReceiver := ""
		portalMXID := ""
		if portal != nil {
			portalKeyID = string(portal.PortalKey.ID)
			portalKeyReceiver = string(portal.PortalKey.Receiver)
			portalMXID = portal.MXID.String()
		}
		log.Debug().
			Str("message_id", strings.TrimSpace(string(msg.ID))).
			Str("event_id", msg.MXID.String()).
			Str("room_id", string(msg.Room.ID)).
			Str("room_receiver", string(msg.Room.Receiver)).
			Str("portal_key_id", portalKeyID).
			Str("portal_key_receiver", portalKeyReceiver).
			Str("portal_mxid", portalMXID).
			Msg("Skipping AI transcript persistence because portal scope is nil")
		return nil
	}
	if strings.TrimSpace(string(msg.ID)) == "" {
		log.Debug().
			Str("event_id", msg.MXID.String()).
			Str("bridge_id", scope.bridgeID).
			Str("login_id", scope.loginID).
			Str("portal_id", scope.portalID).
			Msg("Skipping AI transcript persistence because message ID is empty")
		return nil
	}
	meta, ok := msg.Metadata.(*MessageMetadata)
	if !ok || meta == nil {
		log.Debug().
			Str("message_id", string(msg.ID)).
			Str("event_id", msg.MXID.String()).
			Str("bridge_id", scope.bridgeID).
			Str("login_id", scope.loginID).
			Str("portal_id", scope.portalID).
			Msg("Skipping AI transcript persistence because message metadata is missing or unexpected")
		return nil
	}
	log.Debug().
		Str("message_id", string(msg.ID)).
		Str("event_id", msg.MXID.String()).
		Str("sender_id", string(msg.SenderID)).
		Str("bridge_id", scope.bridgeID).
		Str("login_id", scope.loginID).
		Str("portal_id", scope.portalID).
		Str("meta", transcriptMetaSummary(meta)).
		Msg("Persisting AI transcript message")
	payload, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	createdAt := msg.Timestamp.UnixMilli()
	if msg.Timestamp.IsZero() {
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
	if err == nil {
		log.Debug().
			Str("message_id", string(msg.ID)).
			Str("event_id", msg.MXID.String()).
			Str("bridge_id", scope.bridgeID).
			Str("login_id", scope.loginID).
			Str("portal_id", scope.portalID).
			Msg("Persisted AI transcript message")
	}
	return err
}

func loadAITranscriptMessage(ctx context.Context, portal *bridgev2.Portal, messageID networkid.MessageID) (*database.Message, error) {
	messages, err := loadAITranscriptMessages(ctx, portal, []networkid.MessageID{messageID}, 1)
	if err != nil || len(messages) == 0 {
		return nil, err
	}
	return messages[0], nil
}

func deleteAITranscriptMessage(ctx context.Context, portal *bridgev2.Portal, messageID networkid.MessageID, eventID id.EventID) error {
	scope := portalScopeForPortal(portal)
	if scope == nil {
		return nil
	}
	messageIDStr := strings.TrimSpace(string(messageID))
	eventIDStr := strings.TrimSpace(eventID.String())
	if messageIDStr == "" && eventIDStr == "" {
		return nil
	}
	query := `
		DELETE FROM ` + aiTranscriptTable + `
		WHERE bridge_id=$1 AND login_id=$2 AND portal_id=$3
	`
	args := []any{scope.bridgeID, scope.loginID, scope.portalID}
	switch {
	case messageIDStr != "" && eventIDStr != "":
		args = append(args, messageIDStr, eventIDStr)
		query += ` AND (message_id=$4 OR event_id=$5)`
	case messageIDStr != "":
		args = append(args, messageIDStr)
		query += ` AND message_id=$4`
	default:
		args = append(args, eventIDStr)
		query += ` AND event_id=$4`
	}
	_, err := scope.db.Exec(ctx, query, args...)
	return err
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
	scope := portalScopeForPortal(portal)
	log := oc.loggerForContext(ctx).With().
		Str("portal_key_id", string(portal.PortalKey.ID)).
		Str("portal_key_receiver", string(portal.PortalKey.Receiver)).
		Str("portal_mxid", portal.MXID.String()).
		Int("history_limit", limit).
		Logger()
	if scope == nil {
		log.Debug().Msg("Skipping AI history load because portal scope is nil")
		return nil, nil
	}
	messages, err := loadAITranscriptMessages(ctx, portal, nil, limit)
	if err != nil {
		log.Warn().
			Err(err).
			Str("bridge_id", scope.bridgeID).
			Str("login_id", scope.loginID).
			Str("portal_id", scope.portalID).
			Msg("Failed to load AI transcript history")
		return nil, err
	}
	for _, msg := range messages {
		if msg != nil {
			msg.Room = portal.PortalKey
		}
	}
	log.Debug().
		Str("bridge_id", scope.bridgeID).
		Str("login_id", scope.loginID).
		Str("portal_id", scope.portalID).
		Int("history_count", len(messages)).
		Str("history_sample", transcriptHistorySummary(messages, 3)).
		Msg("Loaded AI transcript history")
	return messages, nil
}
