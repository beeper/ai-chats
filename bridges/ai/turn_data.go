package ai

import (
	"strings"

	"github.com/beeper/agentremote"
	"github.com/beeper/agentremote/pkg/shared/streamui"
	"github.com/beeper/agentremote/sdk"
)

func canonicalTurnData(meta *MessageMetadata) (sdk.TurnData, bool) {
	if meta == nil || meta.CanonicalTurnSchema != sdk.CanonicalTurnDataSchemaV1 || len(meta.CanonicalTurnData) == 0 {
		return sdk.TurnData{}, false
	}
	return sdk.DecodeTurnData(meta.CanonicalTurnData)
}

func turnDataFromStreamingState(state *streamingState, uiMessage map[string]any) sdk.TurnData {
	return sdk.BuildTurnDataFromUIMessage(uiMessage, sdk.TurnDataBuildOptions{
		ID:   state.turnID,
		Role: "assistant",
		Metadata: map[string]any{
			"turn_id":             state.turnID,
			"finish_reason":       state.finishReason,
			"prompt_tokens":       state.promptTokens,
			"completion_tokens":   state.completionTokens,
			"reasoning_tokens":    state.reasoningTokens,
			"response_id":         state.responseID,
			"started_at_ms":       state.startedAtMs,
			"completed_at_ms":     state.completedAtMs,
			"first_token_at_ms":   state.firstTokenAtMs,
			"network_message_id":  state.networkMessageID,
			"initial_event_id":    state.initialEventID,
			"source_event_id":     state.sourceEventID,
			"generated_file_refs": agentremote.GeneratedFileRefsFromParts(state.generatedFiles),
		},
		Text:      state.accumulated.String(),
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
	uiMessage := streamui.SnapshotCanonicalUIMessage(&state.ui)
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

func buildTurnDataMetadata(state *streamingState, meta *PortalMetadata) map[string]any {
	if state == nil {
		return nil
	}
	modelID := ""
	if meta != nil && meta.ResolvedTarget != nil {
		modelID = strings.TrimSpace(meta.ResolvedTarget.ModelID)
	}
	return map[string]any{
		"turn_id":           state.turnID,
		"agent_id":          state.agentID,
		"model":             modelID,
		"finish_reason":     state.finishReason,
		"prompt_tokens":     state.promptTokens,
		"completion_tokens": state.completionTokens,
		"reasoning_tokens":  state.reasoningTokens,
		"total_tokens":      state.totalTokens,
		"started_at_ms":     state.startedAtMs,
		"first_token_at_ms": state.firstTokenAtMs,
		"completed_at_ms":   state.completedAtMs,
	}
}
