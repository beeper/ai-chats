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
	mode := streamtransport.ResolveMode(cc.connector.Config.Bridge.StreamingTransport)
	if mode == streamtransport.ModeDebouncedEdit {
		return mode
	}
	if cc.streamFallbackToDebounced.Load() {
		return streamtransport.ModeDebouncedEdit
	}
	return mode
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
	partType, _ := part["type"].(string)
	switch partType {
	case "text-delta", "reasoning-delta", "text-end", "reasoning-end":
		cc.sendDebouncedStreamEdit(ctx, portal, state, false)
	case "finish", "abort", "error":
		cc.sendDebouncedStreamEdit(ctx, portal, state, true)
		if cc.streamEditGate != nil {
			cc.streamEditGate.Clear(state.turnID)
		}
	}
}

func (cc *CodexClient) fallbackStreamTransportToDebounced(ctx context.Context, reason string, err error) {
	if cc == nil {
		return
	}
	if !cc.streamFallbackToDebounced.CompareAndSwap(false, true) {
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
