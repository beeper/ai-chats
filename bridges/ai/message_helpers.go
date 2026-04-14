package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
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

func (oc *AIClient) upsertTransportPortalMessage(
	ctx context.Context,
	portal *bridgev2.Portal,
	msg *database.Message,
) error {
	if oc == nil || oc.UserLogin == nil || oc.UserLogin.Bridge == nil || oc.UserLogin.Bridge.DB == nil || oc.UserLogin.Bridge.DB.Message == nil {
		return fmt.Errorf("bridge message database unavailable")
	}
	if portal == nil || msg == nil {
		return fmt.Errorf("portal or message is nil")
	}

	portal, err := resolvePortalForAIDB(ctx, oc, portal)
	if err != nil {
		return err
	}
	if portal == nil {
		return fmt.Errorf("canonical portal unavailable")
	}

	db := oc.UserLogin.Bridge.DB.Message
	transport := *msg
	transport.Room = portal.PortalKey
	transport.Metadata = &MessageMetadata{}

	if transport.MXID != "" {
		existing, err := db.GetPartByMXID(ctx, transport.MXID)
		if err != nil {
			return err
		}
		if existing != nil && existing.Room == portal.PortalKey {
			existing.Room = transport.Room
			if transport.ID != "" {
				existing.ID = transport.ID
			}
			if transport.PartID != "" {
				existing.PartID = transport.PartID
			}
			if transport.SenderID != "" {
				existing.SenderID = transport.SenderID
			}
			if !transport.Timestamp.IsZero() {
				existing.Timestamp = transport.Timestamp
			}
			if transport.SendTxnID != "" {
				existing.SendTxnID = transport.SendTxnID
			}
			existing.Metadata = &MessageMetadata{}
			return db.Update(ctx, existing)
		}
	}

	return db.Insert(ctx, &transport)
}
