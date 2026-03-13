package opencode

import (
	"context"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/format"

	"github.com/beeper/agentremote"
	"github.com/beeper/agentremote/bridges/ai/msgconv"
	"github.com/beeper/agentremote/pkg/matrixevents"
	"github.com/beeper/agentremote/pkg/shared/maputil"
	"github.com/beeper/agentremote/pkg/shared/streamui"
	"github.com/beeper/agentremote/pkg/shared/stringutil"
	"github.com/beeper/agentremote/turns"
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
	uiMessage := streamui.SnapshotCanonicalUIMessage(&state.ui)
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
	base := ts.UnixMilli() * 1000
	if base <= 0 {
		base = time.Now().UnixMilli() * 1000
	}
	if state == nil {
		return base
	}
	if base <= state.lastRemoteEventOrder {
		base = state.lastRemoteEventOrder + 1
	}
	state.lastRemoteEventOrder = base
	return base
}

func (oc *OpenCodeClient) buildStreamDBMetadata(state *openCodeStreamState) *MessageMetadata {
	if state == nil {
		return nil
	}
	uiMessage := oc.currentCanonicalUIMessage(state)
	thinking := agentremote.CanonicalReasoningText(agentremote.NormalizeUIParts(uiMessage["parts"]))
	return &MessageMetadata{
		BaseMessageMetadata: agentremote.BaseMessageMetadata{
			Role:               stringutil.FirstNonEmpty(state.role, "assistant"),
			Body:               stringutil.FirstNonEmpty(state.visible.String(), state.accumulated.String()),
			FinishReason:       state.finishReason,
			PromptTokens:       state.promptTokens,
			CompletionTokens:   state.completionTokens,
			ReasoningTokens:    state.reasoningTokens,
			TurnID:             state.turnID,
			AgentID:            state.agentID,
			CanonicalSchema:    "ai-sdk-ui-message-v1",
			CanonicalUIMessage: uiMessage,
			StartedAtMs:        state.startedAtMs,
			CompletedAtMs:      state.completedAtMs,
			ThinkingContent:    thinking,
			ToolCalls:          agentremote.CanonicalToolCalls(agentremote.NormalizeUIParts(uiMessage["parts"]), "opencode"),
			GeneratedFiles:     agentremote.CanonicalGeneratedFiles(agentremote.NormalizeUIParts(uiMessage["parts"])),
		},
		SessionID:       state.sessionID,
		MessageID:       state.messageID,
		ParentMessageID: state.parentMessageID,
		Agent:           state.agent,
		ModelID:         state.modelID,
		ProviderID:      state.providerID,
		Mode:            state.mode,
		ErrorText:       state.errorText,
		Cost:            state.cost,
		TotalTokens:     state.totalTokens,
	}
}

func (oc *OpenCodeClient) persistStreamDBMetadata(ctx context.Context, portal *bridgev2.Portal, state *openCodeStreamState, meta *MessageMetadata) {
	if oc == nil || portal == nil || state == nil || meta == nil {
		return
	}
	agentremote.UpdateExistingMessageMetadata(
		ctx,
		oc.UserLogin,
		portal,
		state.networkMessageID,
		state.initialEventID,
		meta,
		oc.Log(),
		"Failed to load OpenCode stream message for metadata update",
		"Failed to persist OpenCode stream metadata",
	)
}

func (oc *OpenCodeClient) queueFinalStreamEdit(ctx context.Context, portal *bridgev2.Portal, state *openCodeStreamState) {
	if oc == nil || portal == nil || portal.MXID == "" || state == nil || state.networkMessageID == "" {
		return
	}
	body := strings.TrimSpace(state.visible.String())
	if body == "" {
		body = strings.TrimSpace(state.accumulated.String())
	}
	if body == "" {
		body = "..."
	}
	rendered := format.RenderMarkdown(body, true, true)
	uiMessage := oc.currentCanonicalUIMessage(state)
	topLevelExtra := map[string]any{
		matrixevents.BeeperAIKey:        uiMessage,
		"com.beeper.dont_render_edited": true,
		"m.mentions":                    map[string]any{},
	}

	pmeta := oc.PortalMeta(portal)
	instanceID := ""
	if pmeta != nil {
		instanceID = pmeta.InstanceID
	}
	sender := oc.SenderForOpenCode(instanceID, false)
	eventTS := openCodeStreamEventTimestamp(state, true)
	oc.UserLogin.QueueRemoteEvent(&OpenCodeRemoteEdit{
		Portal:        portal.PortalKey,
		Sender:        sender,
		TargetMessage: state.networkMessageID,
		Timestamp:     eventTS,
		StreamOrder:   openCodeNextStreamOrder(state, eventTS),
		LogKey:        "opencode_edit_target",
		PreBuilt: turns.BuildRenderedConvertedEdit(turns.RenderedMarkdownContent{
			Body:          rendered.Body,
			Format:        rendered.Format,
			FormattedBody: rendered.FormattedBody,
		}, topLevelExtra),
	})
}
