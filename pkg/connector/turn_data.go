package connector

import (
	"strings"

	"github.com/beeper/ai-chats/pkg/shared/aihelpers"
	"github.com/beeper/ai-chats/pkg/shared/streamui"
)

func canonicalTurnData(meta *MessageMetadata) (aihelpers.TurnData, bool) {
	if meta == nil || len(meta.CanonicalTurnData) == 0 {
		return aihelpers.TurnData{}, false
	}
	return aihelpers.DecodeTurnData(meta.CanonicalTurnData)
}

func buildCanonicalTurnData(
	state *streamingState,
	linkPreviews []map[string]any,
) aihelpers.TurnData {
	if state == nil {
		return aihelpers.TurnData{}
	}
	uiMessage := map[string]any(nil)
	if state.turn != nil {
		uiMessage = streamui.SnapshotUIMessage(state.turn.UIState())
	}
	artifactParts := buildSourceParts(state.sourceCitations, state.sourceDocuments, nil)
	artifactParts = append(artifactParts, linkPreviews...)
	return aihelpers.BuildTurnDataFromUIMessage(uiMessage, aihelpers.TurnDataBuildOptions{
		ID:             currentStreamingTurnID(state),
		Role:           "assistant",
		Metadata:       currentStreamingTurnMetadata(state),
		Text:           displayStreamingText(state),
		Reasoning:      state.reasoning.String(),
		ToolCalls:      state.toolCalls,
		GeneratedFiles: aihelpers.GeneratedFileRefsFromParts(state.generatedFiles),
		ArtifactParts:  artifactParts,
	})
}

func canonicalResponseStatus(state *streamingState) string {
	if state == nil {
		return ""
	}
	if state.stop.Load() != nil {
		return "cancelled"
	}
	status := strings.TrimSpace(state.responseStatus)
	if state.completedAtMs == 0 {
		return status
	}

	switch status {
	case "completed", "failed", "incomplete", "cancelled":
		return status
	}

	if strings.TrimSpace(state.responseID) == "" {
		return status
	}

	switch strings.TrimSpace(state.finishReason) {
	case "", "stop":
		return "completed"
	case "cancelled":
		return "cancelled"
	case "error":
		return "failed"
	default:
		return status
	}
}

func currentStreamingTurnID(state *streamingState) string {
	if state == nil || state.turn == nil {
		return ""
	}
	return state.turn.ID()
}

func currentStreamingTurnMetadata(state *streamingState) map[string]any {
	if state == nil {
		return nil
	}
	networkMessageID := ""
	initialEventID := ""
	if state.turn != nil {
		networkMessageID = string(state.turn.NetworkMessageID())
		initialEventID = state.turn.InitialEventID().String()
	}
	return buildAssistantTurnMetadata(state, currentStreamingTurnID(state), networkMessageID, initialEventID)
}
