package opencode

import (
	"maunium.net/go/mautrix/bridgev2/database"

	"github.com/beeper/agentremote"
	bridgesdk "github.com/beeper/agentremote/sdk"
)

type MessageMetadata struct {
	agentremote.BaseMessageMetadata
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

// MessageMetadataParams holds all fields needed to construct a MessageMetadata.
// Both streaming and backfill code paths populate this struct, then call
// buildMessageMetadataFromParams to produce the final value.
type MessageMetadataParams struct {
	Role             string
	Body             string
	FinishReason     string
	PromptTokens     int64
	CompletionTokens int64
	ReasoningTokens  int64
	TurnID           string
	AgentID          string
	UIMessage        map[string]any
	StartedAtMs      int64
	CompletedAtMs    int64
	SessionID        string
	MessageID        string
	ParentMessageID  string
	Agent            string
	ModelID          string
	ProviderID       string
	Mode             string
	ErrorText        string
	Cost             float64
	TotalTokens      int64
}

func buildMessageMetadataFromParams(p MessageMetadataParams) *MessageMetadata {
	snapshot := bridgesdk.BuildTurnSnapshot(p.UIMessage, bridgesdk.TurnDataBuildOptions{
		ID:   p.TurnID,
		Role: p.Role,
		Text: p.Body,
		Metadata: map[string]any{
			"turn_id":           p.TurnID,
			"agent_id":          p.AgentID,
			"finish_reason":     p.FinishReason,
			"prompt_tokens":     p.PromptTokens,
			"completion_tokens": p.CompletionTokens,
			"reasoning_tokens":  p.ReasoningTokens,
			"started_at_ms":     p.StartedAtMs,
			"completed_at_ms":   p.CompletedAtMs,
		},
	}, "opencode")
	return &MessageMetadata{
		BaseMessageMetadata: agentremote.BaseMessageMetadata{
			Role:              p.Role,
			Body:              snapshot.Body,
			FinishReason:      p.FinishReason,
			PromptTokens:      p.PromptTokens,
			CompletionTokens:  p.CompletionTokens,
			ReasoningTokens:   p.ReasoningTokens,
			TurnID:            p.TurnID,
			AgentID:           p.AgentID,
			CanonicalTurnData: snapshot.TurnData.ToMap(),
			StartedAtMs:       p.StartedAtMs,
			CompletedAtMs:     p.CompletedAtMs,
			ThinkingContent:   snapshot.ThinkingContent,
			ToolCalls:         snapshot.ToolCalls,
			GeneratedFiles:    snapshot.GeneratedFiles,
		},
		SessionID:       p.SessionID,
		MessageID:       p.MessageID,
		ParentMessageID: p.ParentMessageID,
		Agent:           p.Agent,
		ModelID:         p.ModelID,
		ProviderID:      p.ProviderID,
		Mode:            p.Mode,
		ErrorText:       p.ErrorText,
		Cost:            p.Cost,
		TotalTokens:     p.TotalTokens,
	}
}

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
