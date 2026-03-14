package ai

import (
	"context"
	"strings"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote/bridges/ai/msgconv"
	"github.com/beeper/agentremote/turns"
)

func (oc *AIClient) emitUIFinish(ctx context.Context, portal *bridgev2.Portal, state *streamingState, meta *PortalMetadata) {
	if state == nil {
		return
	}
	ui := oc.uiEmitter(state)
	finishReason := msgconv.MapFinishReason(state.finishReason)
	ui.EmitUIFinish(ctx, portal, finishReason, oc.buildUIMessageMetadata(state, meta, true))
	if state.session != nil {
		state.session.End(ctx, mapTurnEndReason(finishReason))
		state.session = nil
	}

	// Debounced done summary: log the finish only when the stream start was previously logged.
	if state.loggedStreamStart {
		oc.loggerForContext(ctx).Info().
			Str("turn_id", strings.TrimSpace(state.turnID)).
			Int("events_sent", state.sequenceNum).
			Msg("Finished streaming events")
	}
}

func mapTurnEndReason(reason string) turns.EndReason {
	switch reason {
	case "error":
		return turns.EndReasonError
	case "disconnect":
		return turns.EndReasonDisconnect
	case "stop", "length", "content-filter", "tool-calls", "other":
		return turns.EndReasonFinish
	default:
		return turns.EndReasonFinish
	}
}
