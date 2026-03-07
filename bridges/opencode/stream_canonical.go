package opencode

import (
	"context"
	"strconv"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/format"

	"github.com/beeper/ai-bridge/pkg/bridgeadapter"
	"github.com/beeper/ai-bridge/pkg/connector/msgconv"
	"github.com/beeper/ai-bridge/pkg/matrixevents"
	"github.com/beeper/ai-bridge/pkg/shared/streamui"
)

func (oc *OpenCodeClient) applyStreamMessageMetadata(state *openCodeStreamState, metadata map[string]any) {
	if state == nil || len(metadata) == 0 {
		return
	}
	if value := stringMapValue(metadata, "role"); value != "" {
		state.role = value
	}
	if value := stringMapValue(metadata, "session_id"); value != "" {
		state.sessionID = value
	}
	if value := stringMapValue(metadata, "message_id"); value != "" {
		state.messageID = value
	}
	if value := stringMapValue(metadata, "parent_message_id"); value != "" {
		state.parentMessageID = value
	}
	if value := stringMapValue(metadata, "agent"); value != "" {
		state.agent = value
	}
	if value := stringMapValue(metadata, "model_id"); value != "" {
		state.modelID = value
	}
	if value := stringMapValue(metadata, "provider_id"); value != "" {
		state.providerID = value
	}
	if value := stringMapValue(metadata, "mode"); value != "" {
		state.mode = value
	}
	if value := stringMapValue(metadata, "finish_reason"); value != "" {
		state.finishReason = value
	}
	if value := stringMapValue(metadata, "error_text"); value != "" {
		state.errorText = value
	}
	if value, ok := int64MapValue(metadata, "started_at"); ok {
		state.startedAtMs = value
	}
	if value, ok := int64MapValue(metadata, "completed_at"); ok {
		state.completedAtMs = value
	}
	if value, ok := int64MapValue(metadata, "prompt_tokens"); ok {
		state.promptTokens = value
	}
	if value, ok := int64MapValue(metadata, "completion_tokens"); ok {
		state.completionTokens = value
	}
	if value, ok := int64MapValue(metadata, "reasoning_tokens"); ok {
		state.reasoningTokens = value
	}
	if value, ok := int64MapValue(metadata, "total_tokens"); ok {
		state.totalTokens = value
	}
	if value, ok := float64MapValue(metadata, "cost"); ok {
		state.cost = value
	}
}

func (oc *OpenCodeClient) currentCanonicalUIMessage(state *openCodeStreamState) map[string]any {
	if state == nil {
		return nil
	}
	uiMessage := streamui.SnapshotCanonicalUIMessage(&state.ui)
	if len(uiMessage) == 0 {
		return msgconv.BuildUIMessage(msgconv.UIMessageParams{
			TurnID: state.turnID,
			Role:   "assistant",
			Metadata: msgconv.BuildUIMessageMetadata(msgconv.UIMessageMetadataParams{
				TurnID:        state.turnID,
				AgentID:       state.agentID,
				FinishReason:  state.finishReason,
				StartedAtMs:   state.startedAtMs,
				CompletedAtMs: state.completedAtMs,
				IncludeUsage:  true,
			}),
		})
	}
	metadata, _ := uiMessage["metadata"].(map[string]any)
	uiMessage["metadata"] = msgconv.MergeUIMessageMetadata(metadata, msgconv.BuildUIMessageMetadata(msgconv.UIMessageMetadataParams{
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
	}))
	return uiMessage
}

func (oc *OpenCodeClient) buildStreamDBMetadata(state *openCodeStreamState) *MessageMetadata {
	if state == nil {
		return nil
	}
	uiMessage := oc.currentCanonicalUIMessage(state)
	thinking := canonicalReasoningText(uiMessage)
	return &MessageMetadata{
		Role:               firstNonEmpty(state.role, "assistant"),
		Body:               firstNonEmpty(state.visible.String(), state.accumulated.String()),
		SessionID:          state.sessionID,
		MessageID:          state.messageID,
		ParentMessageID:    state.parentMessageID,
		Agent:              state.agent,
		ModelID:            state.modelID,
		ProviderID:         state.providerID,
		Mode:               state.mode,
		FinishReason:       state.finishReason,
		ErrorText:          state.errorText,
		Cost:               state.cost,
		PromptTokens:       state.promptTokens,
		CompletionTokens:   state.completionTokens,
		ReasoningTokens:    state.reasoningTokens,
		TotalTokens:        state.totalTokens,
		TurnID:             state.turnID,
		AgentID:            state.agentID,
		CanonicalSchema:    "ai-sdk-ui-message-v1",
		CanonicalUIMessage: uiMessage,
		StartedAtMs:        state.startedAtMs,
		CompletedAtMs:      state.completedAtMs,
		ThinkingContent:    thinking,
		ToolCalls:          canonicalToolCalls(uiMessage),
		GeneratedFiles:     canonicalGeneratedFiles(uiMessage),
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
	if err != nil || existing == nil {
		return
	}
	existing.Metadata = meta
	_ = oc.UserLogin.Bridge.DB.Message.Update(ctx, existing)
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
		portal:        portal.PortalKey,
		sender:        sender,
		targetMessage: state.networkMessageID,
		timestamp:     time.Now(),
		preBuilt: &bridgev2.ConvertedEdit{
			ModifiedParts: []*bridgev2.ConvertedEditPart{{
				Type: event.EventMessage,
				Content: &event.MessageEventContent{
					MsgType:       event.MsgText,
					Body:          rendered.Body,
					Format:        rendered.Format,
					FormattedBody: rendered.FormattedBody,
				},
				Extra:         map[string]any{"m.mentions": map[string]any{}},
				TopLevelExtra: topLevelExtra,
			}},
		},
	})
}

func stringMapValue(values map[string]any, key string) string {
	raw := values[key]
	if value, ok := raw.(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func int64MapValue(values map[string]any, key string) (int64, bool) {
	raw, ok := values[key]
	if !ok || raw == nil {
		return 0, false
	}
	switch value := raw.(type) {
	case int:
		return int64(value), true
	case int32:
		return int64(value), true
	case int64:
		return value, true
	case float64:
		return int64(value), true
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err == nil {
			return parsed, true
		}
	}
	return 0, false
}

func float64MapValue(values map[string]any, key string) (float64, bool) {
	raw, ok := values[key]
	if !ok || raw == nil {
		return 0, false
	}
	switch value := raw.(type) {
	case float64:
		return value, true
	case float32:
		return float64(value), true
	case int:
		return float64(value), true
	case int64:
		return float64(value), true
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err == nil {
			return parsed, true
		}
	}
	return 0, false
}

func canonicalReasoningText(uiMessage map[string]any) string {
	parts, _ := uiMessage["parts"].([]any)
	var sb strings.Builder
	for _, raw := range parts {
		part, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if strings.TrimSpace(stringMapValue(part, "type")) != "reasoning" {
			continue
		}
		text := stringMapValue(part, "text")
		if text == "" {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(text)
	}
	return sb.String()
}

func canonicalGeneratedFiles(uiMessage map[string]any) []bridgeadapter.GeneratedFileRef {
	parts, _ := uiMessage["parts"].([]any)
	var refs []bridgeadapter.GeneratedFileRef
	for _, raw := range parts {
		part, ok := raw.(map[string]any)
		if !ok || strings.TrimSpace(stringMapValue(part, "type")) != "file" {
			continue
		}
		url := stringMapValue(part, "url")
		if url == "" {
			continue
		}
		refs = append(refs, bridgeadapter.GeneratedFileRef{
			URL:      url,
			MimeType: firstNonEmpty(stringMapValue(part, "mediaType"), "application/octet-stream"),
		})
	}
	return refs
}

func canonicalToolCalls(uiMessage map[string]any) []bridgeadapter.ToolCallMetadata {
	parts, _ := uiMessage["parts"].([]any)
	var calls []bridgeadapter.ToolCallMetadata
	for _, raw := range parts {
		part, ok := raw.(map[string]any)
		if !ok || strings.TrimSpace(stringMapValue(part, "type")) != "dynamic-tool" {
			continue
		}
		call := bridgeadapter.ToolCallMetadata{
			CallID:   stringMapValue(part, "toolCallId"),
			ToolName: stringMapValue(part, "toolName"),
			ToolType: "opencode",
			Status:   stringMapValue(part, "state"),
		}
		if input, ok := part["input"].(map[string]any); ok {
			call.Input = input
		}
		if output, ok := part["output"].(map[string]any); ok {
			call.Output = output
		} else if text := stringMapValue(part, "output"); text != "" {
			call.Output = map[string]any{"text": text}
		}
		switch call.Status {
		case "output-available":
			call.ResultStatus = "completed"
		case "output-denied":
			call.ResultStatus = "denied"
		case "output-error":
			call.ResultStatus = "error"
			call.ErrorMessage = stringMapValue(part, "errorText")
		case "approval-requested":
			call.ResultStatus = "pending_approval"
		default:
			call.ResultStatus = call.Status
		}
		if call.CallID != "" {
			calls = append(calls, call)
		}
	}
	return calls
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
