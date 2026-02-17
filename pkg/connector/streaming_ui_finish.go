package connector

import (
	"context"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
)

func (oc *AIClient) emitUIError(ctx context.Context, portal *bridgev2.Portal, state *streamingState, errText string) {
	if errText == "" {
		errText = "Unknown error"
	}
	oc.emitStreamEvent(ctx, portal, state, map[string]any{
		"type":      "error",
		"errorText": errText,
	})
}

func (oc *AIClient) emitUIFinish(ctx context.Context, portal *bridgev2.Portal, state *streamingState, meta *PortalMetadata) {
	if state.uiFinished {
		return
	}
	state.uiFinished = true
	if state.uiTextID != "" {
		oc.emitStreamEvent(ctx, portal, state, map[string]any{
			"type": "text-end",
			"id":   state.uiTextID,
		})
		state.uiTextID = ""
	}
	if state.uiReasoningID != "" {
		oc.emitStreamEvent(ctx, portal, state, map[string]any{
			"type": "reasoning-end",
			"id":   state.uiReasoningID,
		})
		state.uiReasoningID = ""
	}
	oc.emitUIStepFinish(ctx, portal, state)
	// Finalize any un-finished tool calls before sending finish.
	// If a stream ends (error, cancel, timeout) while a tool is mid-execution,
	// these tools would otherwise stay in a non-terminal state forever.
	for toolCallID := range state.uiToolStarted {
		if !state.uiToolOutputFinalized[toolCallID] {
			oc.emitUIToolOutputError(ctx, portal, state, toolCallID, "cancelled", false)
		}
	}
	oc.emitStreamEvent(ctx, portal, state, map[string]any{
		"type":            "finish",
		"finishReason":    mapFinishReason(state.finishReason),
		"messageMetadata": oc.buildUIMessageMetadata(state, meta, true),
	})

	// Debounced done summary: always log the finish with event count.
	if state.loggedStreamStart {
		oc.loggerForContext(ctx).Info().
			Str("turn_id", strings.TrimSpace(state.turnID)).
			Int("events_sent", state.sequenceNum).
			Msg("Finished streaming ephemeral events")
	}
}
