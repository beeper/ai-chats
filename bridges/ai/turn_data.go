package ai

import (
	"strings"

	"github.com/beeper/agentremote"
	"github.com/beeper/agentremote/pkg/shared/streamui"
	"github.com/beeper/agentremote/sdk"
)

func canonicalTurnData(meta *MessageMetadata) (sdk.TurnData, bool) {
	if meta == nil || len(meta.CanonicalTurnData) == 0 {
		return sdk.TurnData{}, false
	}
	return sdk.DecodeTurnData(meta.CanonicalTurnData)
}

func turnDataFromStreamingState(state *streamingState, uiMessage map[string]any) sdk.TurnData {
	turnID := ""
	networkMessageID := ""
	initialEventID := ""
	if state != nil && state.turn != nil {
		turnID = state.turn.ID()
		networkMessageID = string(state.turn.NetworkMessageID())
		initialEventID = state.turn.InitialEventID().String()
	}
	return sdk.BuildTurnDataFromUIMessage(uiMessage, sdk.TurnDataBuildOptions{
		ID:        turnID,
		Role:      "assistant",
		Metadata:  buildAssistantTurnMetadata(state, turnID, networkMessageID, initialEventID),
		Text:      displayStreamingText(state),
		Reasoning: state.reasoning.String(),
		ToolCalls: state.toolCalls,
	})
}

func buildCanonicalTurnData(
	state *streamingState,
	meta *PortalMetadata,
	linkPreviews []map[string]any,
) sdk.TurnData {
	if state == nil {
		return sdk.TurnData{}
	}
	uiMessage := streamui.SnapshotUIMessage(currentStreamingUIState(state))
	td := turnDataFromStreamingState(state, uiMessage)
	artifactParts := buildSourceParts(state.sourceCitations, state.sourceDocuments, nil)
	artifactParts = append(artifactParts, linkPreviews...)
	return sdk.BuildTurnDataFromUIMessage(sdk.UIMessageFromTurnData(td), sdk.TurnDataBuildOptions{
		ID:             td.ID,
		Role:           td.Role,
		Metadata:       buildTurnDataMetadata(state, meta),
		GeneratedFiles: agentremote.GeneratedFileRefsFromParts(state.generatedFiles),
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

func buildTurnDataMetadata(state *streamingState, _ *PortalMetadata) map[string]any {
	if state == nil {
		return nil
	}
	turnID := ""
	if state.turn != nil {
		turnID = state.turn.ID()
	}
	return buildAssistantTurnMetadata(state, turnID, "", "")
}
