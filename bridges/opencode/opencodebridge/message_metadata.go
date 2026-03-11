package opencodebridge

import (
	"maunium.net/go/mautrix/bridgev2/database"

	"github.com/beeper/agentremote/pkg/bridgeadapter"
)

type MessageMetadata struct {
	bridgeadapter.BaseMessageMetadata
	SessionID       string  `json:"session_id,omitempty"`
	MessageID       string  `json:"message_id,omitempty"`
	ParentMessageID string  `json:"parent_message_id,omitempty"`
	Agent           string  `json:"agent,omitempty"`
	ModelID         string  `json:"model_id,omitempty"`
	ProviderID      string  `json:"provider_id,omitempty"`
	Mode            string  `json:"mode,omitempty"`
	ErrorText       string  `json:"error_text,omitempty"`
	Cost            float64 `json:"cost,omitempty"`
	TotalTokens     int64   `json:"total_tokens,omitempty"`
}

type ToolCallMetadata = bridgeadapter.ToolCallMetadata

type GeneratedFileRef = bridgeadapter.GeneratedFileRef

var _ database.MetaMerger = (*MessageMetadata)(nil)

func (mm *MessageMetadata) CopyFrom(other any) {
	src, ok := other.(*MessageMetadata)
	if !ok || src == nil {
		return
	}
	mm.CopyFromBase(&src.BaseMessageMetadata)
	if src.SessionID != "" {
		mm.SessionID = src.SessionID
	}
	if src.MessageID != "" {
		mm.MessageID = src.MessageID
	}
	if src.ParentMessageID != "" {
		mm.ParentMessageID = src.ParentMessageID
	}
	if src.Agent != "" {
		mm.Agent = src.Agent
	}
	if src.ModelID != "" {
		mm.ModelID = src.ModelID
	}
	if src.ProviderID != "" {
		mm.ProviderID = src.ProviderID
	}
	if src.Mode != "" {
		mm.Mode = src.Mode
	}
	if src.ErrorText != "" {
		mm.ErrorText = src.ErrorText
	}
	if src.Cost != 0 {
		mm.Cost = src.Cost
	}
	if src.TotalTokens != 0 {
		mm.TotalTokens = src.TotalTokens
	}
}
