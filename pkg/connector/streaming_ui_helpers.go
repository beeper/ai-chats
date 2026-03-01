package connector

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/beeper/ai-bridge/pkg/connector/msgconv"
)

func (oc *AIClient) buildUIMessageMetadata(state *streamingState, meta *PortalMetadata, includeUsage bool) map[string]any {
	return msgconv.BuildUIMessageMetadata(msgconv.UIMessageMetadataParams{
		TurnID:           state.turnID,
		AgentID:          state.agentID,
		Model:            oc.effectiveModel(meta),
		FinishReason:     state.finishReason,
		PromptTokens:     state.promptTokens,
		CompletionTokens: state.completionTokens,
		ReasoningTokens:  state.reasoningTokens,
		TotalTokens:      state.totalTokens,
		StartedAtMs:      state.startedAtMs,
		FirstTokenAtMs:   state.firstTokenAtMs,
		CompletedAtMs:    state.completedAtMs,
		IncludeUsage:     includeUsage,
	})
}

func mapFinishReason(reason string) string {
	return msgconv.MapFinishReason(reason)
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
	if state.visibleAccumulated.Len() == 0 {
		state.needsTextSeparator = false
		return rawDelta
	}

	// Only insert when both sides are non-whitespace; avoids double-spacing if the model already
	// starts the new round with whitespace/newlines.
	vis := state.visibleAccumulated.String()
	last, _ := utf8.DecodeLastRuneInString(vis)
	first, _ := utf8.DecodeRuneInString(rawDelta)
	state.needsTextSeparator = false
	if unicode.IsSpace(last) || unicode.IsSpace(first) {
		return rawDelta
	}
	// Newline is rendered as whitespace in Markdown/HTML, preventing word run-ons.
	return "\n" + rawDelta
}
