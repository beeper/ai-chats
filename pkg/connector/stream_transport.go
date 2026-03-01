package connector

import (
	"context"
	"time"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/ai-bridge/pkg/shared/streamtransport"
)

func (oc *AIClient) streamTransportMode() streamtransport.Mode {
	if oc == nil || oc.connector == nil {
		return streamtransport.DefaultMode
	}
	return streamtransport.ResolveMode(oc.connector.Config.Bridge.StreamingTransport)
}

func (oc *AIClient) streamEditDebounceDuration() time.Duration {
	if oc == nil || oc.connector == nil {
		return streamtransport.ResolveDebounceDuration(0)
	}
	return streamtransport.ResolveDebounceDuration(oc.connector.Config.Bridge.StreamingDebounce)
}

func (oc *AIClient) sendDebouncedStreamEdit(ctx context.Context, portal *bridgev2.Portal, state *streamingState, force bool) {
	if oc == nil || state == nil {
		return
	}
	streamtransport.SendDebouncedEdit(ctx, streamtransport.DebouncedEditParams{
		Portal:         portal,
		Force:          force,
		SuppressSend:   state.suppressSend,
		VisibleBody:    state.visibleAccumulated.String(),
		FallbackBody:   state.accumulated.String(),
		InitialEventID: state.initialEventID,
		TurnID:         state.turnID,
		Gate:           &oc.streamEditGate,
		Debounce:       oc.streamEditDebounceDuration(),
		Intent:         oc.getModelIntent(ctx, portal),
		Log:            oc.loggerForContext(ctx),
	})
}
