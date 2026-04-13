package ai

import (
	"github.com/beeper/agentremote/pkg/shared/jsonutil"
	"github.com/beeper/agentremote/sdk"
)

type assistantStopMetadata struct {
	Reason             string `json:"reason,omitempty"`
	Scope              string `json:"scope,omitempty"`
	TargetKind         string `json:"target_kind,omitempty"`
	TargetEventID      string `json:"target_event_id,omitempty"`
	RequestedByEventID string `json:"requested_by_event_id,omitempty"`
	RequestedVia       string `json:"requested_via,omitempty"`
}

type assistantTurnMetadata struct {
	FinishReason      string                 `json:"finish_reason,omitempty"`
	ResponseID        string                 `json:"response_id,omitempty"`
	ResponseStatus    string                 `json:"response_status,omitempty"`
	NetworkMessageID  string                 `json:"network_message_id,omitempty"`
	InitialEventID    string                 `json:"initial_event_id,omitempty"`
	SourceEventID     string                 `json:"source_event_id,omitempty"`
	GeneratedFileRefs []GeneratedFileRef     `json:"generated_file_refs,omitempty"`
	Stop              *assistantStopMetadata `json:"stop,omitempty"`
}

func buildAssistantTurnMetadata(state *streamingState, turnID, networkMessageID, initialEventID string) map[string]any {
	if state == nil {
		return nil
	}
	extras := map[string]any{}
	usageExtras := map[string]any{}
	if state.respondingContextLimit > 0 {
		usageExtras["context_limit"] = float64(state.respondingContextLimit)
	}
	if state.promptTokens > 0 {
		usageExtras["prompt_tokens"] = float64(state.promptTokens)
	}
	if state.completionTokens > 0 {
		usageExtras["completion_tokens"] = float64(state.completionTokens)
	}
	if state.reasoningTokens > 0 {
		usageExtras["reasoning_tokens"] = float64(state.reasoningTokens)
	}
	if state.totalTokens > 0 {
		usageExtras["total_tokens"] = float64(state.totalTokens)
	}
	if len(usageExtras) > 0 {
		extras["usage"] = usageExtras
	}
	return sdk.BuildUIMessageMetadata(sdk.UIMessageMetadataParams{
		TurnID:           turnID,
		AgentID:          state.respondingAgentID,
		Model:            state.respondingModelID,
		FinishReason:     state.finishReason,
		PromptTokens:     state.promptTokens,
		CompletionTokens: state.completionTokens,
		ReasoningTokens:  state.reasoningTokens,
		TotalTokens:      state.totalTokens,
		StartedAtMs:      state.startedAtMs,
		FirstTokenAtMs:   state.firstTokenAtMs,
		CompletedAtMs:    state.completedAtMs,
		IncludeUsage:     true,
		Extras: jsonutil.MergeRecursive(jsonutil.ToMap(assistantTurnMetadata{
			FinishReason:      state.finishReason,
			ResponseID:        state.responseID,
			ResponseStatus:    canonicalResponseStatus(state),
			NetworkMessageID:  networkMessageID,
			InitialEventID:    initialEventID,
			SourceEventID:     state.sourceEventID().String(),
			GeneratedFileRefs: sdk.GeneratedFileRefsFromParts(state.generatedFiles),
			Stop:              state.stop.Load(),
		}), extras),
	})
}
