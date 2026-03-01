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
	mode := streamtransport.ResolveMode(oc.connector.Config.Bridge.StreamingTransport)
	if mode == streamtransport.ModeDebouncedEdit {
		return mode
	}
	if oc.streamFallbackToDebounced.Load() {
		return streamtransport.ModeDebouncedEdit
	}
	return mode
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
	partType, _ := part["type"].(string)
	switch partType {
	case "text-delta", "reasoning-delta", "text-end", "reasoning-end":
		oc.sendDebouncedStreamEdit(ctx, portal, state, false)
	case "finish", "abort", "error":
		oc.sendDebouncedStreamEdit(ctx, portal, state, true)
		if oc.streamEditGate != nil {
			oc.streamEditGate.Clear(state.turnID)
		}
	}
}

func (oc *AIClient) fallbackStreamTransportToDebounced(ctx context.Context, reason string, err error) {
	if oc == nil {
		return
	}
	if !oc.streamFallbackToDebounced.CompareAndSwap(false, true) {
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
