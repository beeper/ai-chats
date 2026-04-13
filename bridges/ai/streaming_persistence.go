package ai

import (
	"context"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/sdk"
)

func (oc *AIClient) buildStreamingMessageMetadata(state *streamingState, meta *PortalMetadata, uiMessage map[string]any) *MessageMetadata {
	if state == nil {
		return nil
	}
	turn := state.turn
	turnID := ""
	if turn != nil {
		turnID = turn.ID()
	}
	if len(uiMessage) == 0 && turn != nil {
		uiMessage = oc.buildStreamUIMessage(state, meta, nil)
	}
	snapshot := sdk.TurnSnapshot{}
	if turn != nil {
		snapshot = sdk.SnapshotFromTurnData(buildCanonicalTurnData(state, meta, nil), "ai")
	} else {
		snapshot = sdk.BuildTurnSnapshot(uiMessage, sdk.TurnDataBuildOptions{
			ID:             turnID,
			Role:           "assistant",
			Text:           displayStreamingText(state),
			Reasoning:      state.reasoning.String(),
			ToolCalls:      state.toolCalls,
			GeneratedFiles: sdk.GeneratedFileRefsFromParts(state.generatedFiles),
		}, "ai")
		if len(uiMessage) == 0 {
			snapshot.UIMessage = nil
			snapshot.TurnData = sdk.TurnData{}
		}
	}
	modelID := state.respondingModelID
	if modelID == "" {
		modelID = oc.effectiveModel(meta)
	}
	bundle := sdk.BuildAssistantMetadataBundle(sdk.AssistantMetadataBundleParams{
		Snapshot:           snapshot,
		FinishReason:       state.finishReason,
		TurnID:             turnID,
		AgentID:            state.agentID,
		StartedAtMs:        state.startedAtMs,
		CompletedAtMs:      state.completedAtMs,
		PromptTokens:       state.promptTokens,
		CompletionTokens:   state.completionTokens,
		ReasoningTokens:    state.reasoningTokens,
		Model:              modelID,
		CompletionID:       state.responseID,
		FirstTokenAtMs:     state.firstTokenAtMs,
		ThinkingTokenCount: thinkingTokenCount(modelID, state.reasoning.String()),
	})
	return &MessageMetadata{
		BaseMessageMetadata:      bundle.Base,
		AssistantMessageMetadata: bundle.Assistant,
	}
}

func (oc *AIClient) noteStreamingPersistenceSideEffects(ctx context.Context, portal *bridgev2.Portal, state *streamingState, meta *PortalMetadata) {
	if state == nil {
		return
	}
	if meta != nil && portal != nil && (state.promptTokens > 0 || state.completionTokens > 0) {
		meta.CompactionLastPromptTokens = state.promptTokens
		meta.CompactionLastCompletionTokens = state.completionTokens
		meta.CompactionLastUsageAt = time.Now().UnixMilli()
		oc.savePortalQuiet(ctx, portal, "compaction usage snapshot")
	}
	oc.notifySessionMutation(ctx, portal, meta, false)
}

// saveAssistantMessage saves the completed assistant message to the database.
// The bridge message row remains transport-only; the canonical assistant turn
// is mirrored into the AI-owned turn store.
func (oc *AIClient) saveAssistantMessage(
	ctx context.Context,
	log zerolog.Logger,
	portal *bridgev2.Portal,
	state *streamingState,
	meta *PortalMetadata,
) {
	if state == nil {
		return
	}
	uiMessage := map[string]any(nil)
	if state.turn != nil {
		uiMessage = oc.buildStreamUIMessage(state, meta, nil)
	}
	fullMeta := oc.buildStreamingMessageMetadata(state, meta, uiMessage)
	turn := state.turn
	networkMessageID := networkid.MessageID("")
	initialEventID := id.EventID("")
	if turn != nil {
		networkMessageID = turn.NetworkMessageID()
		initialEventID = turn.InitialEventID()
	}

	messageID := networkMessageID
	if messageID == "" && initialEventID != "" {
		messageID = sdk.MatrixMessageID(initialEventID)
	}
	if messageID != "" && portal != nil {
		turnMsg := &database.Message{
			ID:   messageID,
			MXID: initialEventID,
			Room: portal.PortalKey,
			SenderID: func() networkid.UserID {
				if state.respondingGhostID != "" {
					return networkid.UserID(state.respondingGhostID)
				}
				return modelUserID(oc.effectiveModel(meta))
			}(),
			Metadata: cloneMessageMetadata(fullMeta),
		}
		if state.completedAtMs == 0 {
			turnMsg.Timestamp = time.Now()
		} else {
			turnMsg.Timestamp = time.UnixMilli(state.completedAtMs)
			if turnMsg.Timestamp.IsZero() {
				turnMsg.Timestamp = time.Now()
			}
		}
		if err := oc.persistAIConversationMessage(ctx, portal, turnMsg); err != nil {
			log.Warn().Err(err).Str("msg_id", string(messageID)).Msg("Failed to persist assistant turn")
		}
	}
	oc.noteStreamingPersistenceSideEffects(ctx, portal, state, meta)
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
