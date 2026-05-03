package connector

import (
	"maps"
	"strings"

	"maunium.net/go/mautrix/event"

	"github.com/beeper/ai-chats/pkg/shared/aihelpers"
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
	td := buildCanonicalTurnData(state, nil)
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
	turnData := buildCanonicalTurnData(state, linkPreviewParts)
	return aihelpers.UIMessageFromTurnData(turnData)
}
