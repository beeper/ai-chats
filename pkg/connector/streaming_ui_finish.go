package connector

import (
	"context"
	"strings"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/ai-bridge/pkg/shared/streamtransport"
)

func (oc *AIClient) emitUIFinish(ctx context.Context, portal *bridgev2.Portal, state *streamingState, meta *PortalMetadata) {
	if state == nil {
		return
	}
	ui := oc.uiEmitter(state)
	ui.EmitUIFinish(ctx, portal, mapFinishReason(state.finishReason), oc.buildUIMessageMetadata(state, meta, true))
	if state.session != nil {
		state.session.End(ctx, streamtransport.EndReason(mapFinishReason(state.finishReason)))
		state.session = nil
	}

	// Debounced done summary: always log the finish with event count.
	if state.loggedStreamStart {
		oc.loggerForContext(ctx).Info().
			Str("turn_id", strings.TrimSpace(state.turnID)).
			Int("events_sent", state.sequenceNum).
			Msg("Finished streaming events")
	}
}
