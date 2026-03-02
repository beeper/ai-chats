package connector

import (
	"context"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"

	"github.com/beeper/ai-bridge/pkg/bridgeadapter"
	"github.com/beeper/ai-bridge/pkg/connector/msgconv"
	"github.com/beeper/ai-bridge/pkg/shared/citations"
)

// saveAssistantMessage saves the completed assistant message to the database.
// When sendViaPortal was used (state.networkMessageID is set), the DB row already exists
// from SendConvertedMessage — this function updates the metadata with full streaming results.
// Otherwise, it falls back to inserting a new row.
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

	fullMeta := &MessageMetadata{
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
		ThinkingContent:    state.reasoning.String(),
		ThinkingTokenCount: thinkingTokenCount(modelID, state.reasoning.String()),
		PromptTokens:       state.promptTokens,
		CompletionTokens:   state.completionTokens,
		ReasoningTokens:    state.reasoningTokens,
	}

	// If the message was sent via sendViaPortal, the DB row already exists — update it.
	if state.networkMessageID != "" {
		existing, err := oc.UserLogin.Bridge.DB.Message.GetPartByMXID(ctx, state.initialEventID)
		if err == nil && existing != nil {
			existing.Metadata = fullMeta
			if err := oc.UserLogin.Bridge.DB.Message.Update(ctx, existing); err != nil {
				log.Warn().Err(err).Str("msg_id", string(existing.ID)).Msg("Failed to update assistant message metadata")
			} else {
				log.Debug().Str("msg_id", string(existing.ID)).Msg("Updated assistant message metadata")
			}
		} else {
			log.Warn().Err(err).Stringer("mxid", state.initialEventID).Msg("Could not find existing DB row for update, falling back to insert")
			oc.insertAssistantMessage(ctx, log, portal, state, modelID, fullMeta)
		}
	} else {
		oc.insertAssistantMessage(ctx, log, portal, state, modelID, fullMeta)
	}

	usageMetaUpdated := false
	if meta != nil && (state.promptTokens > 0 || state.completionTokens > 0) {
		if meta.ModuleMeta == nil {
			meta.ModuleMeta = make(map[string]any, 4)
		}
		meta.ModuleMeta["compaction_last_prompt_tokens"] = state.promptTokens
		meta.ModuleMeta["compaction_last_completion_tokens"] = state.completionTokens
		meta.ModuleMeta["compaction_last_usage_at"] = time.Now().UnixMilli()
		usageMetaUpdated = true
	}
	if usageMetaUpdated && portal != nil {
		oc.savePortalQuiet(ctx, portal, "compaction usage snapshot")
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

// insertAssistantMessage is the fallback path for saving assistant messages when no
// pre-existing DB row was created by sendViaPortal.
func (oc *AIClient) insertAssistantMessage(
	ctx context.Context,
	log zerolog.Logger,
	portal *bridgev2.Portal,
	state *streamingState,
	modelID string,
	meta *MessageMetadata,
) {
	assistantMsg := &database.Message{
		ID:        bridgeadapter.MatrixMessageID(state.initialEventID),
		Room:      portal.PortalKey,
		SenderID:  modelUserID(modelID),
		MXID:      state.initialEventID,
		Timestamp: time.Now(),
		Metadata:  meta,
	}
	if err := oc.UserLogin.Bridge.DB.Message.Insert(ctx, assistantMsg); err != nil {
		log.Warn().Err(err).Msg("Failed to insert assistant message to database")
	} else {
		log.Debug().Str("msg_id", string(assistantMsg.ID)).Msg("Inserted assistant message to database")
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

	parts := msgconv.ContentParts(state.accumulated.String(), strings.TrimSpace(state.reasoning.String()))
	if toolParts := msgconv.ToolCallParts(state.toolCalls, string(ToolTypeProvider), string(ResultStatusSuccess), string(ResultStatusDenied)); len(toolParts) > 0 {
		parts = append(parts, toolParts...)
	}

	messageID := state.turnID
	if strings.TrimSpace(messageID) == "" && state.initialEventID != "" {
		messageID = state.initialEventID.String()
	}

	metadata := msgconv.BuildUIMessageMetadata(msgconv.UIMessageMetadataParams{
		TurnID:           state.turnID,
		AgentID:          state.agentID,
		Model:            oc.effectiveModel(meta),
		FinishReason:     state.finishReason,
		PromptTokens:     state.promptTokens,
		CompletionTokens: state.completionTokens,
		ReasoningTokens:  state.reasoningTokens,
		StartedAtMs:      state.startedAtMs,
		FirstTokenAtMs:   state.firstTokenAtMs,
		CompletedAtMs:    state.completedAtMs,
		IncludeUsage:     true,
	})

	return msgconv.BuildUIMessage(msgconv.UIMessageParams{
		TurnID:     messageID,
		Role:       "assistant",
		Metadata:   metadata,
		Parts:      parts,
		SourceURLs: buildSourceParts(state.sourceCitations, state.sourceDocuments, nil),
		FileParts:  citations.GeneratedFilesToParts(state.generatedFiles),
	})
}
