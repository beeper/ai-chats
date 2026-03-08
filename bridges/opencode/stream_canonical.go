package opencode

import (
	"context"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/format"

	"github.com/beeper/ai-bridge/bridges/opencode/opencodebridge"
	"github.com/beeper/ai-bridge/pkg/bridgeadapter"
	"github.com/beeper/ai-bridge/pkg/connector/msgconv"
	"github.com/beeper/ai-bridge/pkg/matrixevents"
	"github.com/beeper/ai-bridge/pkg/shared/maputil"
	"github.com/beeper/ai-bridge/pkg/shared/streamtransport"
	"github.com/beeper/ai-bridge/pkg/shared/streamui"
	"github.com/beeper/ai-bridge/pkg/shared/stringutil"
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

func (oc *OpenCodeClient) buildStreamDBMetadata(state *openCodeStreamState) *MessageMetadata {
	if state == nil {
		return nil
	}
	uiMessage := oc.currentCanonicalUIMessage(state)
	thinking := opencodebridge.CanonicalReasoningText(uiMessage)
	return &MessageMetadata{
		BaseMessageMetadata: bridgeadapter.BaseMessageMetadata{
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
			ToolCalls:          opencodebridge.CanonicalToolCalls(uiMessage),
			GeneratedFiles:     opencodebridge.CanonicalGeneratedFiles(uiMessage),
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
	if oc == nil || oc.UserLogin == nil || oc.UserLogin.Bridge == nil || portal == nil || state == nil || meta == nil {
		return
	}
	receiver := portal.Receiver
	if receiver == "" {
		receiver = oc.UserLogin.ID
	}
	var existing *database.Message
	var err error
	if state.networkMessageID != "" {
		existing, err = oc.UserLogin.Bridge.DB.Message.GetPartByID(ctx, receiver, state.networkMessageID, networkid.PartID("0"))
	}
	if existing == nil && state.initialEventID != "" {
		existing, err = oc.UserLogin.Bridge.DB.Message.GetPartByMXID(ctx, state.initialEventID)
	}
	if err != nil {
		oc.Log().Warn().
			Err(err).
			Str("receiver", string(receiver)).
			Str("network_message_id", string(state.networkMessageID)).
			Stringer("initial_event_id", state.initialEventID).
			Msg("Failed to load OpenCode stream message for metadata update")
		return
	}
	if existing == nil {
		return
	}
	existing.Metadata = meta
	if err := oc.UserLogin.Bridge.DB.Message.Update(ctx, existing); err != nil {
		oc.Log().Warn().
			Err(err).
			Str("receiver", string(receiver)).
			Str("network_message_id", string(state.networkMessageID)).
			Stringer("initial_event_id", state.initialEventID).
			Msg("Failed to persist OpenCode stream metadata")
	}
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
	oc.UserLogin.QueueRemoteEvent(&OpenCodeRemoteEdit{
		Portal:        portal.PortalKey,
		Sender:        sender,
		TargetMessage: state.networkMessageID,
		Timestamp:     time.Now(),
		LogKey:        "opencode_edit_target",
		PreBuilt: streamtransport.BuildRenderedConvertedEdit(streamtransport.RenderedMarkdownContent{
			Body:          rendered.Body,
			Format:        rendered.Format,
			FormattedBody: rendered.FormattedBody,
		}, topLevelExtra),
	})
}
