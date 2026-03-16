package ai

import (
	"maps"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"maunium.net/go/mautrix/event"

	"github.com/beeper/agentremote/pkg/shared/streamui"
	"github.com/beeper/agentremote/pkg/shared/stringutil"
	"github.com/beeper/agentremote/sdk"
)

func currentStreamingUIState(state *streamingState) *streamui.UIState {
	if state == nil || state.turn == nil {
		return nil
	}
	return state.turn.UIState()
}

func rawStreamingText(state *streamingState) string {
	if state == nil {
		return ""
	}
	return state.accumulated.String()
}

func visibleStreamingText(state *streamingState) string {
	if state == nil {
		return ""
	}
	if state.turn == nil {
		return ""
	}
	return state.turn.VisibleText()
}

func displayStreamingText(state *streamingState) string {
	if state == nil {
		return ""
	}
	if text := visibleStreamingText(state); strings.TrimSpace(text) != "" {
		return text
	}
	return rawStreamingText(state)
}

func (oc *AIClient) buildUIMessageMetadata(state *streamingState, meta *PortalMetadata, includeUsage bool) map[string]any {
	td := buildCanonicalTurnData(state, meta, nil)
	metadata := td.Metadata
	if !includeUsage && len(metadata) > 0 {
		metadata = maps.Clone(metadata)
		delete(metadata, "usage")
		delete(metadata, "prompt_tokens")
		delete(metadata, "completion_tokens")
		delete(metadata, "reasoning_tokens")
		delete(metadata, "total_tokens")
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

func buildCompactFinalUIMessage(uiMessage map[string]any) map[string]any {
	if len(uiMessage) == 0 {
		return nil
	}
	out := map[string]any{}
	for key, value := range uiMessage {
		if key != "parts" {
			out[key] = value
		}
	}

	rawParts, ok := uiMessage["parts"].([]any)
	if !ok {
		if typed, ok := uiMessage["parts"].([]map[string]any); ok {
			rawParts = make([]any, 0, len(typed))
			for _, part := range typed {
				rawParts = append(rawParts, part)
			}
		}
	}
	if len(rawParts) == 0 {
		return out
	}

	parts := make([]any, 0, len(rawParts))
	for _, raw := range rawParts {
		part, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		partType := strings.TrimSpace(stringutil.StringValue(part["type"]))
		switch partType {
		case "text", "reasoning", "step-start":
			continue
		default:
			parts = append(parts, part)
		}
	}
	if len(parts) > 0 {
		out["parts"] = slices.Clone(parts)
	}
	return out
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

func maybePrependTextSeparator(state *streamingState, rawDelta string) string {
	if state == nil || !state.needsTextSeparator {
		return rawDelta
	}
	// Keep waiting until we see a non-whitespace delta; some providers stream whitespace separately.
	if strings.TrimSpace(rawDelta) == "" {
		return rawDelta
	}
	// If we don't have any visible text yet, don't inject anything.
	visible := visibleStreamingText(state)
	if visible == "" {
		state.needsTextSeparator = false
		return rawDelta
	}

	// Only insert when both sides are non-whitespace; avoids double-spacing if the model already
	// starts the new round with whitespace/newlines.
	last, _ := utf8.DecodeLastRuneInString(visible)
	first, _ := utf8.DecodeRuneInString(rawDelta)
	state.needsTextSeparator = false
	if unicode.IsSpace(last) || unicode.IsSpace(first) {
		return rawDelta
	}
	// Newline is rendered as whitespace in Markdown/HTML, preventing word run-ons.
	return "\n" + rawDelta
}
