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

func (oc *AIClient) buildStreamingMessageMetadata(state *streamingState, meta *PortalMetadata, turnData sdk.TurnData) *MessageMetadata {
	if state == nil {
		return nil
	}
	turn := state.turn
	turnID := ""
	if turn != nil {
		turnID = turn.ID()
	}
	modelID := state.respondingModelID
	if modelID == "" {
		modelID = oc.effectiveModel(meta)
	}
	bundle := sdk.BuildAssistantMetadataBundle(sdk.AssistantMetadataBundleParams{
		TurnData:           turnData,
		ToolType:           "ai",
		FinishReason:       state.finishReason,
		TurnID:             turnID,
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
	var turnData sdk.TurnData
	if state.turn != nil {
		turnData = buildCanonicalTurnData(state, nil)
	}
	fullMeta := oc.buildStreamingMessageMetadata(state, meta, turnData)
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
