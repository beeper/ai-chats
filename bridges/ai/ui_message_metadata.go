package ai

import (
	"github.com/beeper/agentremote"
	"github.com/beeper/agentremote/pkg/shared/jsonutil"
)

type assistantUsageMetadata struct {
	ContextLimit     int64 `json:"context_limit,omitempty"`
	PromptTokens     int64 `json:"prompt_tokens,omitempty"`
	CompletionTokens int64 `json:"completion_tokens,omitempty"`
	ReasoningTokens  int64 `json:"reasoning_tokens,omitempty"`
	TotalTokens      int64 `json:"total_tokens,omitempty"`
}

type assistantStopMetadata struct {
	Reason             string `json:"reason,omitempty"`
	Scope              string `json:"scope,omitempty"`
	TargetKind         string `json:"target_kind,omitempty"`
	TargetEventID      string `json:"target_event_id,omitempty"`
	RequestedByEventID string `json:"requested_by_event_id,omitempty"`
	RequestedVia       string `json:"requested_via,omitempty"`
}

type assistantTurnMetadata struct {
	TurnID            string                  `json:"turn_id,omitempty"`
	AgentID           string                  `json:"agent_id,omitempty"`
	Model             string                  `json:"model,omitempty"`
	FinishReason      string                  `json:"finish_reason,omitempty"`
	ResponseID        string                  `json:"response_id,omitempty"`
	ResponseStatus    string                  `json:"response_status,omitempty"`
	StartedAtMs       int64                   `json:"started_at_ms,omitempty"`
	FirstTokenAtMs    int64                   `json:"first_token_at_ms,omitempty"`
	CompletedAtMs     int64                   `json:"completed_at_ms,omitempty"`
	NetworkMessageID  string                  `json:"network_message_id,omitempty"`
	InitialEventID    string                  `json:"initial_event_id,omitempty"`
	SourceEventID     string                  `json:"source_event_id,omitempty"`
	GeneratedFileRefs []GeneratedFileRef      `json:"generated_file_refs,omitempty"`
	Usage             *assistantUsageMetadata `json:"usage,omitempty"`
	Stop              *assistantStopMetadata  `json:"stop,omitempty"`
}

func buildAssistantUsageMetadata(state *streamingState) *assistantUsageMetadata {
	if state == nil {
		return nil
	}
	usage := &assistantUsageMetadata{
		ContextLimit:     int64(state.respondingContextLimit),
		PromptTokens:     state.promptTokens,
		CompletionTokens: state.completionTokens,
		ReasoningTokens:  state.reasoningTokens,
		TotalTokens:      state.totalTokens,
	}
	if usage.ContextLimit == 0 &&
		usage.PromptTokens == 0 &&
		usage.CompletionTokens == 0 &&
		usage.ReasoningTokens == 0 &&
		usage.TotalTokens == 0 {
		return nil
	}
	return usage
}

func buildAssistantTurnMetadata(state *streamingState, turnID, networkMessageID, initialEventID string) map[string]any {
	if state == nil {
		return nil
	}
	return jsonutil.ToMap(assistantTurnMetadata{
		TurnID:            turnID,
		AgentID:           state.respondingAgentID,
		Model:             state.respondingModelID,
		FinishReason:      state.finishReason,
		ResponseID:        state.responseID,
		ResponseStatus:    canonicalResponseStatus(state),
		StartedAtMs:       state.startedAtMs,
		FirstTokenAtMs:    state.firstTokenAtMs,
		CompletedAtMs:     state.completedAtMs,
		NetworkMessageID:  networkMessageID,
		InitialEventID:    initialEventID,
		SourceEventID:     state.sourceEventID().String(),
		GeneratedFileRefs: agentremote.GeneratedFileRefsFromParts(state.generatedFiles),
		Usage:             buildAssistantUsageMetadata(state),
		Stop:              state.stop.Load(),
	})
}
