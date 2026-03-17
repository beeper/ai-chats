package opencode

import (
	"strings"
	"time"

	"github.com/beeper/agentremote/bridges/ai/msgconv"
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
		state.stream.SetFinishReason(value)
	}
	if value := maputil.StringArg(metadata, "error_text"); value != "" {
		state.stream.SetErrorText(value)
	}
	if value, ok := maputil.NumberArg(metadata, "started_at"); ok {
		state.stream.SetStartedAtMs(int64(value))
	}
	if value, ok := maputil.NumberArg(metadata, "completed_at"); ok {
		state.stream.SetCompletedAtMs(int64(value))
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

func (oc *OpenCodeClient) currentUIMessage(state *openCodeStreamState) map[string]any {
	if state == nil {
		return nil
	}
	uiState := &state.ui
	if state.turn != nil && state.turn.UIState() != nil {
		uiState = state.turn.UIState()
	}
	uiMessage := streamui.SnapshotUIMessage(uiState)
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
		FinishReason:     state.stream.FinishReason(),
		PromptTokens:     state.promptTokens,
		CompletionTokens: state.completionTokens,
		ReasoningTokens:  state.reasoningTokens,
		TotalTokens:      state.totalTokens,
		StartedAtMs:      state.stream.StartedAtMs(),
		CompletedAtMs:    state.stream.CompletedAtMs(),
		IncludeUsage:     true,
	})
}

func (oc *OpenCodeClient) buildStreamDBMetadata(state *openCodeStreamState) *MessageMetadata {
	if state == nil {
		return nil
	}
	uiMessage := oc.currentUIMessage(state)
	return buildMessageMetadataFromParams(MessageMetadataParams{
		Role:             stringutil.FirstNonEmpty(state.role, "assistant"),
		Body:             stringutil.FirstNonEmpty(state.stream.VisibleText(), state.stream.AccumulatedText()),
		FinishReason:     state.stream.FinishReason(),
		PromptTokens:     state.promptTokens,
		CompletionTokens: state.completionTokens,
		ReasoningTokens:  state.reasoningTokens,
		TurnID:           state.turnID,
		AgentID:          state.agentID,
		UIMessage:        uiMessage,
		StartedAtMs:      state.stream.StartedAtMs(),
		CompletedAtMs:    state.stream.CompletedAtMs(),
		SessionID:        state.sessionID,
		MessageID:        state.messageID,
		ParentMessageID:  state.parentMessageID,
		Agent:            state.agent,
		ModelID:          state.modelID,
		ProviderID:       state.providerID,
		Mode:             state.mode,
		ErrorText:        state.stream.ErrorText(),
		Cost:             state.cost,
		TotalTokens:      state.totalTokens,
	})
}

func (oc *OpenCodeClient) buildSDKFinalMetadata(state *openCodeStreamState, finishReason string) any {
	if state == nil {
		return nil
	}
	if trimmed := strings.TrimSpace(finishReason); trimmed != "" {
		state.stream.SetFinishReason(trimmed)
	}
	if state.stream.CompletedAtMs() == 0 {
		state.stream.SetCompletedAtMs(time.Now().UnixMilli())
	}
	return oc.buildStreamDBMetadata(state)
}
