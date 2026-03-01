package connector

import (
	"context"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"

	"github.com/beeper/ai-bridge/pkg/bridgeadapter"
)

// saveAssistantMessage saves the completed assistant message to the database
func (oc *AIClient) saveAssistantMessage(
	ctx context.Context,
	log zerolog.Logger,
	portal *bridgev2.Portal,
	state *streamingState,
	meta *PortalMetadata,
) {
	modelID := oc.effectiveModel(meta)

	// Collect generated file references for multimodal history re-injection.
	var genFiles []GeneratedFileRef
	if len(state.generatedFiles) > 0 {
		genFiles = make([]GeneratedFileRef, 0, len(state.generatedFiles))
		for _, f := range state.generatedFiles {
			genFiles = append(genFiles, GeneratedFileRef{URL: f.URL, MimeType: f.MediaType})
		}
	}

	assistantMsg := &database.Message{
		ID:        bridgeadapter.MatrixMessageID(state.initialEventID),
		Room:      portal.PortalKey,
		SenderID:  modelUserID(modelID),
		MXID:      state.initialEventID,
		Timestamp: time.Now(),
		Metadata: &MessageMetadata{
			Role:               "assistant",
			Body:               state.accumulated.String(),
			CompletionID:       state.responseID,
			FinishReason:       state.finishReason,
			Model:              modelID,
			TurnID:             state.turnID,
			AgentID:            state.agentID,
			ToolCalls:          state.toolCalls,
			StartedAtMs:        state.startedAtMs,
			FirstTokenAtMs:     state.firstTokenAtMs,
			CompletedAtMs:      state.completedAtMs,
			HasToolCalls:       len(state.toolCalls) > 0,
			CanonicalSchema:    "ai-sdk-ui-message-v1",
			CanonicalUIMessage: oc.buildCanonicalUIMessage(state, meta),
			GeneratedFiles:     genFiles,
			// Reasoning fields (only populated by Responses API)
			ThinkingContent:    state.reasoning.String(),
			ThinkingTokenCount: thinkingTokenCount(modelID, state.reasoning.String()),
			PromptTokens:       state.promptTokens,
			CompletionTokens:   state.completionTokens,
			ReasoningTokens:    state.reasoningTokens,
		},
	}
	if err := oc.UserLogin.Bridge.DB.Message.Insert(ctx, assistantMsg); err != nil {
		log.Warn().Err(err).Msg("Failed to save assistant message to database")
	} else {
		log.Debug().Str("msg_id", string(assistantMsg.ID)).Msg("Saved assistant message to database")
	}
	oc.notifySessionMutation(ctx, portal, meta, false)

	// Save LastResponseID for "responses" mode context chaining (OpenAI-only)
	if meta.ConversationMode == "responses" && state.responseID != "" && !oc.isOpenRouterProvider() {
		meta.LastResponseID = state.responseID
		if err := portal.Save(ctx); err != nil {
			log.Warn().Err(err).Msg("Failed to save portal after storing response ID")
		}
	}
}

func thinkingTokenCount(model string, content string) int {
	content = strings.TrimSpace(content)
	if content == "" {
		return 0
	}
	tkm, err := getTokenizer(model)
	if err != nil {
		return len(strings.Fields(content))
	}
	return len(tkm.Encode(content, nil, nil))
}

func (oc *AIClient) buildCanonicalUIMessage(state *streamingState, meta *PortalMetadata) map[string]any {
	if state == nil {
		return nil
	}

	parts := make([]map[string]any, 0, 2+len(state.toolCalls))
	reasoningText := strings.TrimSpace(state.reasoning.String())
	if reasoningText != "" {
		parts = append(parts, map[string]any{
			"type":  "reasoning",
			"text":  reasoningText,
			"state": "done",
		})
	}
	text := state.accumulated.String()
	if text != "" {
		parts = append(parts, map[string]any{
			"type":  "text",
			"text":  text,
			"state": "done",
		})
	}
	for _, tc := range state.toolCalls {
		toolPart := map[string]any{
			"type":       "dynamic-tool",
			"toolName":   tc.ToolName,
			"toolCallId": tc.CallID,
			"input":      tc.Input,
		}
		if tc.ToolType == string(ToolTypeProvider) {
			toolPart["providerExecuted"] = true
		}
		if tc.ResultStatus == string(ResultStatusSuccess) {
			toolPart["state"] = "output-available"
			toolPart["output"] = tc.Output
		} else {
			toolPart["state"] = "output-error"
			if tc.ErrorMessage != "" {
				toolPart["errorText"] = tc.ErrorMessage
			} else if result, ok := tc.Output["result"].(string); ok && result != "" {
				toolPart["errorText"] = result
			}
		}
		parts = append(parts, toolPart)
	}
	if sourceParts := buildSourceParts(state.sourceCitations, state.sourceDocuments, nil); len(sourceParts) > 0 {
		parts = append(parts, sourceParts...)
	}
	if fileParts := generatedFilesToParts(state.generatedFiles); len(fileParts) > 0 {
		parts = append(parts, fileParts...)
	}

	messageID := state.turnID
	if strings.TrimSpace(messageID) == "" && state.initialEventID != "" {
		messageID = state.initialEventID.String()
	}

	metadata := map[string]any{}
	if state.turnID != "" {
		metadata["turn_id"] = state.turnID
	}
	if state.agentID != "" {
		metadata["agent_id"] = state.agentID
	}
	if model := oc.effectiveModel(meta); model != "" {
		metadata["model"] = model
	}
	if state.finishReason != "" {
		metadata["finish_reason"] = mapFinishReason(state.finishReason)
	}
	if state.promptTokens > 0 || state.completionTokens > 0 || state.reasoningTokens > 0 {
		metadata["usage"] = map[string]any{
			"prompt_tokens":     state.promptTokens,
			"completion_tokens": state.completionTokens,
			"reasoning_tokens":  state.reasoningTokens,
		}
	}
	timing := map[string]any{}
	if state.startedAtMs > 0 {
		timing["started_at"] = state.startedAtMs
	}
	if state.firstTokenAtMs > 0 {
		timing["first_token_at"] = state.firstTokenAtMs
	}
	if state.completedAtMs > 0 {
		timing["completed_at"] = state.completedAtMs
	}
	if len(timing) > 0 {
		metadata["timing"] = timing
	}

	uiMessage := map[string]any{
		"id":    messageID,
		"role":  "assistant",
		"parts": parts,
	}
	if len(metadata) > 0 {
		uiMessage["metadata"] = metadata
	}
	return uiMessage
}
