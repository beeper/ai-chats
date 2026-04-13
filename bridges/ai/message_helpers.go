package ai

import (
	"encoding/json"
	"fmt"
	"strings"

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
