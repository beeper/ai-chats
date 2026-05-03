package connector

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

func attachPromptTurnData(meta *MessageMetadata, promptContext PromptContext) *MessageMetadata {
	if meta == nil {
		return nil
	}
	if promptContext.CurrentTurnData.Role == "" {
		meta.CanonicalTurnData = nil
		return meta
	}
	meta.CanonicalTurnData = promptContext.CurrentTurnData.ToMap()
	return meta
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
