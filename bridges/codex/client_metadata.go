package codex

import (
	"context"
	"strings"

	"github.com/beeper/agentremote/pkg/shared/streamui"
	"github.com/beeper/agentremote/sdk"
	"maunium.net/go/mautrix/bridgev2"
)

func (cc *CodexClient) buildUIMessageMetadata(state *streamingState, model string, includeUsage bool, finishReason string) map[string]any {
	if state != nil && strings.TrimSpace(state.currentModel) != "" {
		model = state.currentModel
	}
	return sdk.BuildUIMessageMetadata(sdk.UIMessageMetadataParams{
		TurnID:           state.currentTurnID(),
		AgentID:          state.agentID,
		Model:            strings.TrimSpace(model),
		FinishReason:     finishReason,
		PromptTokens:     state.promptTokens,
		CompletionTokens: state.completionTokens,
		ReasoningTokens:  state.reasoningTokens,
		TotalTokens:      state.totalTokens,
		StartedAtMs:      state.startedAtMs,
		FirstTokenAtMs:   state.firstTokenAtMs,
		CompletedAtMs:    state.completedAtMs,
		IncludeUsage:     includeUsage,
	})
}

func buildMessageMetadata(state *streamingState, turnID string, model string, finishReason string, uiMessage map[string]any) *MessageMetadata {
	if state != nil && strings.TrimSpace(state.currentModel) != "" {
		model = state.currentModel
	}
	turnData := sdk.BuildTurnDataFromUIMessage(uiMessage, sdk.TurnDataBuildOptions{
		ID:             turnID,
		Role:           "assistant",
		Text:           state.accumulated.String(),
		Reasoning:      state.reasoning.String(),
		ToolCalls:      state.toolCalls,
		GeneratedFiles: sdk.GeneratedFileRefsFromParts(state.generatedFiles),
	})
	bundle := sdk.BuildAssistantMetadataBundle(sdk.AssistantMetadataBundleParams{
		TurnData:           turnData,
		ToolType:           "codex",
		FinishReason:       finishReason,
		TurnID:             turnID,
		AgentID:            state.agentID,
		StartedAtMs:        state.startedAtMs,
		CompletedAtMs:      state.completedAtMs,
		PromptTokens:       state.promptTokens,
		CompletionTokens:   state.completionTokens,
		ReasoningTokens:    state.reasoningTokens,
		Model:              model,
		FirstTokenAtMs:     state.firstTokenAtMs,
		ThinkingTokenCount: len(strings.Fields(state.reasoning.String())),
	})
	return &MessageMetadata{
		BaseMessageMetadata:      bundle.Base,
		AssistantMessageMetadata: bundle.Assistant,
	}
}

func (cc *CodexClient) buildSDKFinalMetadata(turn *sdk.Turn, state *streamingState, model string, finishReason string) any {
	if turn == nil || state == nil {
		return &MessageMetadata{}
	}
	return buildMessageMetadata(state, turn.ID(), model, finishReason, streamui.SnapshotUIMessage(turn.UIState()))
}

func (cc *CodexClient) sendSystemNoticeOnce(ctx context.Context, portal *bridgev2.Portal, state *streamingState, key string, message string) {
	key = strings.TrimSpace(key)
	if key == "" || state == nil {
		cc.sendSystemNotice(ctx, portal, message)
		return
	}
	if state.codexTimelineNotices == nil {
		state.codexTimelineNotices = make(map[string]bool)
	}
	if state.codexTimelineNotices[key] {
		return
	}
	state.codexTimelineNotices[key] = true
	cc.sendSystemNotice(ctx, portal, message)
}
