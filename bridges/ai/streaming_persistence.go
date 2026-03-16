package ai

import (
	"context"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote"
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
			GeneratedFiles: agentremote.GeneratedFileRefsFromParts(state.generatedFiles),
		}, "ai")
		if len(uiMessage) == 0 {
			snapshot.UIMessage = nil
			snapshot.TurnData = sdk.TurnData{}
		}
	}
	modelID := oc.effectiveModel(meta)
	canonicalTurnData := map[string]any(nil)
	if len(snapshot.TurnData.ToMap()) > 0 {
		canonicalTurnData = snapshot.TurnData.ToMap()
	}
	return &MessageMetadata{
		BaseMessageMetadata: agentremote.BuildAssistantBaseMetadata(agentremote.AssistantMetadataParams{
			Body:              snapshot.Body,
			FinishReason:      state.finishReason,
			TurnID:            turnID,
			AgentID:           state.agentID,
			ToolCalls:         snapshot.ToolCalls,
			StartedAtMs:       state.startedAtMs,
			CompletedAtMs:     state.completedAtMs,
			GeneratedFiles:    snapshot.GeneratedFiles,
			ThinkingContent:   snapshot.ThinkingContent,
			PromptTokens:      state.promptTokens,
			CompletionTokens:  state.completionTokens,
			ReasoningTokens:   state.reasoningTokens,
			CanonicalTurnData: canonicalTurnData,
		}),
		AssistantMessageMetadata: agentremote.AssistantMessageMetadata{
			CompletionID:       state.responseID,
			Model:              modelID,
			FirstTokenAtMs:     state.firstTokenAtMs,
			HasToolCalls:       len(state.toolCalls) > 0,
			ThinkingTokenCount: thinkingTokenCount(modelID, state.reasoning.String()),
		},
	}
}

func (oc *AIClient) noteStreamingPersistenceSideEffects(ctx context.Context, portal *bridgev2.Portal, state *streamingState, meta *PortalMetadata) {
	if state == nil {
		return
	}
	if meta != nil && portal != nil && (state.promptTokens > 0 || state.completionTokens > 0) {
		meta.SetModuleMeta("compaction_last_prompt_tokens", state.promptTokens)
		meta.SetModuleMeta("compaction_last_completion_tokens", state.completionTokens)
		meta.SetModuleMeta("compaction_last_usage_at", time.Now().UnixMilli())
		oc.savePortalQuiet(ctx, portal, "compaction usage snapshot")
	}
	oc.notifySessionMutation(ctx, portal, meta, false)
}

// saveAssistantMessage saves the completed assistant message to the database.
// When sendViaPortal was used (state.turn.NetworkMessageID() is set), the DB row already exists
// from SendConvertedMessage — this function updates the metadata with full streaming results.
// Otherwise, it falls back to inserting a new row.
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

	agentremote.UpsertAssistantMessage(ctx, agentremote.UpsertAssistantMessageParams{
		Login:            oc.UserLogin,
		Portal:           portal,
		SenderID:         modelUserID(oc.effectiveModel(meta)),
		NetworkMessageID: networkMessageID,
		InitialEventID:   initialEventID,
		Metadata:         fullMeta,
		Logger:           log,
	})
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
