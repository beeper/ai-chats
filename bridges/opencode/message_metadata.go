package opencode

import (
	"maunium.net/go/mautrix/bridgev2/database"

	"github.com/beeper/agentremote/sdk"
)

type MessageMetadata struct {
	sdk.BaseMessageMetadata
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

var _ database.MetaMerger = (*MessageMetadata)(nil)

func (mm *MessageMetadata) CopyFrom(other any) {
	src, ok := other.(*MessageMetadata)
	if !ok || src == nil {
		return
	}
	mm.CopyFromBase(&src.BaseMessageMetadata)
	sdk.CopyNonZero(&mm.SessionID, src.SessionID)
	sdk.CopyNonZero(&mm.MessageID, src.MessageID)
	sdk.CopyNonZero(&mm.ParentMessageID, src.ParentMessageID)
	sdk.CopyNonZero(&mm.Agent, src.Agent)
	sdk.CopyNonZero(&mm.ModelID, src.ModelID)
	sdk.CopyNonZero(&mm.ProviderID, src.ProviderID)
	sdk.CopyNonZero(&mm.Mode, src.Mode)
	sdk.CopyNonZero(&mm.ErrorText, src.ErrorText)
	sdk.CopyNonZero(&mm.Cost, src.Cost)
	sdk.CopyNonZero(&mm.TotalTokens, src.TotalTokens)
}
