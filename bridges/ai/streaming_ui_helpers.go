package ai

import (
	"maps"
	"strings"

	"maunium.net/go/mautrix/event"

	"github.com/beeper/agentremote/sdk"
)

func displayStreamingText(state *streamingState) string {
	if state == nil {
		return ""
	}
	if state.turn != nil {
		if text := state.turn.VisibleText(); strings.TrimSpace(text) != "" {
			return text
		}
	}
	if text := state.accumulated.String(); strings.TrimSpace(text) != "" {
		return text
	}
	return ""
}

func (oc *AIClient) buildUIMessageMetadata(state *streamingState, meta *PortalMetadata, includeUsage bool) map[string]any {
	td := buildCanonicalTurnData(state, meta, nil)
	metadata := td.Metadata
	if !includeUsage && len(metadata) > 0 {
		metadata = maps.Clone(metadata)
		delete(metadata, "usage")
	}
	return metadata
}

// buildStreamUIMessage constructs the UI message projection for streaming edits and persistence.
// linkPreviews may be nil for intermediate saves.
func (oc *AIClient) buildStreamUIMessage(state *streamingState, meta *PortalMetadata, linkPreviews []*event.BeeperLinkPreview) map[string]any {
	if state == nil {
		return nil
	}
	linkPreviewParts := buildSourceParts(nil, nil, linkPreviews)
	turnData := buildCanonicalTurnData(state, meta, linkPreviewParts)
	return sdk.UIMessageFromTurnData(turnData)
}

func shouldContinueChatToolLoop(finishReason string, toolCallCount int) bool {
	if toolCallCount <= 0 {
		return false
	}
	// Some providers/adapters report inconsistent finish reasons (e.g. "stop") even when
	// tool calls are present in the stream. The presence of tool calls is the reliable
	// signal that we must continue after sending tool results.
	switch strings.ToLower(strings.TrimSpace(finishReason)) {
	case "error", "cancelled":
		return false
	default:
		return true
	}
}
