package codex

import (
	"context"
	"time"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/ai-bridge/pkg/shared/streamtransport"
)

func (cc *CodexClient) streamTransportMode() streamtransport.Mode {
	if cc == nil || cc.connector == nil {
		return streamtransport.DefaultMode
	}
	return streamtransport.ResolveModeWithRuntimeFallback(
		cc.connector.Config.Bridge.StreamingTransport,
		&cc.streamFallbackToDebounced,
	)
}

func (cc *CodexClient) streamEditDebounceDuration() time.Duration {
	if cc == nil || cc.connector == nil {
		return streamtransport.ResolveDebounceDuration(0)
	}
	return streamtransport.ResolveDebounceDuration(cc.connector.Config.Bridge.StreamingDebounce)
}

func (cc *CodexClient) sendDebouncedStreamEdit(ctx context.Context, portal *bridgev2.Portal, state *streamingState, force bool) {
	if cc == nil || state == nil || portal == nil {
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
		Gate:           &cc.streamEditGate,
		Debounce:       cc.streamEditDebounceDuration(),
		Intent:         cc.getCodexIntent(ctx, portal),
		Log:            cc.loggerForContext(ctx),
	})
}

func (cc *CodexClient) emitDebouncedStreamPart(ctx context.Context, portal *bridgev2.Portal, state *streamingState, part map[string]any) {
	if state == nil {
		return
	}
	streamtransport.HandleDebouncedPart(
		part,
		func(force bool) { cc.sendDebouncedStreamEdit(ctx, portal, state, force) },
		func() {
			if cc.streamEditGate != nil {
				cc.streamEditGate.Clear(state.turnID)
			}
		},
	)
}

func (cc *CodexClient) fallbackStreamTransportToDebounced(ctx context.Context, reason string, err error) {
	if cc == nil {
		return
	}
	if !streamtransport.EnableRuntimeFallbackToDebounced(&cc.streamFallbackToDebounced) {
		return
	}
	if err != nil {
		cc.loggerForContext(ctx).Warn().
			Err(err).
			Str("reason", reason).
			Msg("Switching stream transport to debounced_edit for this runtime; ephemeral streaming will be retried after bridge restart")
		return
	}
	cc.loggerForContext(ctx).Warn().
		Str("reason", reason).
		Msg("Switching stream transport to debounced_edit for this runtime; ephemeral streaming will be retried after bridge restart")
}
