package opencode

import (
	"strings"
	"time"

	"github.com/beeper/agentremote/bridges/ai/msgconv"
	"github.com/beeper/agentremote/pkg/shared/backfillutil"
	"github.com/beeper/agentremote/pkg/shared/maputil"
	"github.com/beeper/agentremote/pkg/shared/streamui"
	"github.com/beeper/agentremote/pkg/shared/stringutil"
)

func (oc *OpenCodeClient) applyStreamMessageMetadata(state *openCodeStreamState, metadata map[string]any) {
	if state == nil || len(metadata) == 0 {
		return
	}
	if value := maputil.StringArg(metadata, "role"); value != "" {
		state.role = value
	}
	if value := maputil.StringArg(metadata, "session_id"); value != "" {
		state.sessionID = value
	}
	if value := maputil.StringArg(metadata, "message_id"); value != "" {
		state.messageID = value
	}
	if value := maputil.StringArg(metadata, "parent_message_id"); value != "" {
		state.parentMessageID = value
	}
	if value := maputil.StringArg(metadata, "agent"); value != "" {
		state.agent = value
	}
	if value := maputil.StringArg(metadata, "model_id"); value != "" {
		state.modelID = value
	}
	if value := maputil.StringArg(metadata, "provider_id"); value != "" {
		state.providerID = value
	}
	if value := maputil.StringArg(metadata, "mode"); value != "" {
		state.mode = value
	}
	if value := maputil.StringArg(metadata, "finish_reason"); value != "" {
		state.finishReason = value
	}
	if value := maputil.StringArg(metadata, "error_text"); value != "" {
		state.errorText = value
	}
	if value, ok := maputil.NumberArg(metadata, "started_at"); ok {
		state.startedAtMs = int64(value)
	}
	if value, ok := maputil.NumberArg(metadata, "completed_at"); ok {
		state.completedAtMs = int64(value)
	}
	if value, ok := maputil.NumberArg(metadata, "prompt_tokens"); ok {
		state.promptTokens = int64(value)
	}
	if value, ok := maputil.NumberArg(metadata, "completion_tokens"); ok {
		state.completionTokens = int64(value)
	}
	if value, ok := maputil.NumberArg(metadata, "reasoning_tokens"); ok {
		state.reasoningTokens = int64(value)
	}
	if value, ok := maputil.NumberArg(metadata, "total_tokens"); ok {
		state.totalTokens = int64(value)
	}
	if value, ok := maputil.NumberArg(metadata, "cost"); ok {
		state.cost = value
	}
}

func (oc *OpenCodeClient) currentCanonicalUIMessage(state *openCodeStreamState) map[string]any {
	if state == nil {
		return nil
	}
	uiState := &state.ui
	if state.turn != nil && state.turn.UIState() != nil {
		uiState = state.turn.UIState()
	}
	uiMessage := streamui.SnapshotCanonicalUIMessage(uiState)
	metadata := opencodeUIMessageMetadata(state)
	if len(uiMessage) == 0 {
		return msgconv.BuildUIMessage(msgconv.UIMessageParams{
			TurnID:   state.turnID,
			Role:     "assistant",
			Metadata: metadata,
		})
	}
	existingMetadata, _ := uiMessage["metadata"].(map[string]any)
	uiMessage["metadata"] = msgconv.MergeUIMessageMetadata(existingMetadata, metadata)
	return uiMessage
}

func opencodeUIMessageMetadata(state *openCodeStreamState) map[string]any {
	return msgconv.BuildUIMessageMetadata(msgconv.UIMessageMetadataParams{
		TurnID:           state.turnID,
		AgentID:          state.agentID,
		Model:            state.modelID,
		FinishReason:     state.finishReason,
		PromptTokens:     state.promptTokens,
		CompletionTokens: state.completionTokens,
		ReasoningTokens:  state.reasoningTokens,
		TotalTokens:      state.totalTokens,
		StartedAtMs:      state.startedAtMs,
		CompletedAtMs:    state.completedAtMs,
		IncludeUsage:     true,
	})
}

func openCodeStreamEventTimestamp(state *openCodeStreamState, preferCompleted bool) time.Time {
	if state == nil {
		return time.Now()
	}
	if preferCompleted && state.completedAtMs > 0 {
		return time.UnixMilli(state.completedAtMs)
	}
	if state.startedAtMs > 0 {
		return time.UnixMilli(state.startedAtMs)
	}
	if state.completedAtMs > 0 {
		return time.UnixMilli(state.completedAtMs)
	}
	return time.Now()
}

func openCodeNextStreamOrder(state *openCodeStreamState, ts time.Time) int64 {
	if state == nil {
		return backfillutil.NextStreamOrder(0, ts)
	}
	state.lastRemoteEventOrder = backfillutil.NextStreamOrder(state.lastRemoteEventOrder, ts)
	return state.lastRemoteEventOrder
}

func (oc *OpenCodeClient) buildStreamDBMetadata(state *openCodeStreamState) *MessageMetadata {
	if state == nil {
		return nil
	}
	uiMessage := oc.currentCanonicalUIMessage(state)
	return buildMessageMetadataFromParams(MessageMetadataParams{
		Role:             stringutil.FirstNonEmpty(state.role, "assistant"),
		Body:             stringutil.FirstNonEmpty(state.visible.String(), state.accumulated.String()),
		FinishReason:     state.finishReason,
		PromptTokens:     state.promptTokens,
		CompletionTokens: state.completionTokens,
		ReasoningTokens:  state.reasoningTokens,
		TurnID:           state.turnID,
		AgentID:          state.agentID,
		UIMessage:        uiMessage,
		StartedAtMs:      state.startedAtMs,
		CompletedAtMs:    state.completedAtMs,
		SessionID:        state.sessionID,
		MessageID:        state.messageID,
		ParentMessageID:  state.parentMessageID,
		Agent:            state.agent,
		ModelID:          state.modelID,
		ProviderID:       state.providerID,
		Mode:             state.mode,
		ErrorText:        state.errorText,
		Cost:             state.cost,
		TotalTokens:      state.totalTokens,
	})
}

func (oc *OpenCodeClient) buildSDKFinalMetadata(state *openCodeStreamState, finishReason string) any {
	if state == nil {
		return nil
	}
	if strings.TrimSpace(finishReason) != "" {
		state.finishReason = strings.TrimSpace(finishReason)
	}
	if state.completedAtMs == 0 {
		state.completedAtMs = time.Now().UnixMilli()
	}
	return oc.buildStreamDBMetadata(state)
}
