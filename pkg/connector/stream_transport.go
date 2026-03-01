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
	return streamtransport.ResolveModeWithRuntimeFallback(
		oc.connector.Config.Bridge.StreamingTransport,
		&oc.streamFallbackToDebounced,
	)
}

func (oc *AIClient) streamEditDebounceDuration() time.Duration {
	if oc == nil || oc.connector == nil {
		return streamtransport.ResolveDebounceDuration(0)
	}
	return streamtransport.ResolveDebounceDuration(oc.connector.Config.Bridge.StreamingDebounce)
}

func (oc *AIClient) sendDebouncedStreamEdit(ctx context.Context, portal *bridgev2.Portal, state *streamingState, force bool) {
	if oc == nil || state == nil || portal == nil {
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

func (oc *AIClient) emitDebouncedStreamPart(ctx context.Context, portal *bridgev2.Portal, state *streamingState, part map[string]any) {
	if state == nil {
		return
	}
	streamtransport.HandleDebouncedPart(
		part,
		func(force bool) { oc.sendDebouncedStreamEdit(ctx, portal, state, force) },
		func() {
			if oc.streamEditGate != nil {
				oc.streamEditGate.Clear(state.turnID)
			}
		},
	)
}

func (oc *AIClient) fallbackStreamTransportToDebounced(ctx context.Context, reason string, err error) {
	if oc == nil {
		return
	}
	if !streamtransport.EnableRuntimeFallbackToDebounced(&oc.streamFallbackToDebounced) {
		return
	}
	if err != nil {
		oc.loggerForContext(ctx).Warn().
			Err(err).
			Str("reason", reason).
			Msg("Switching stream transport to debounced_edit for this runtime; ephemeral streaming will be retried after bridge restart")
		return
	}
	oc.loggerForContext(ctx).Warn().
		Str("reason", reason).
		Msg("Switching stream transport to debounced_edit for this runtime; ephemeral streaming will be retried after bridge restart")
}
