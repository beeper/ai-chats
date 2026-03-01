package connector

import (
	"context"
	"strings"

	"github.com/beeper/ai-bridge/pkg/matrixevents"
	"github.com/beeper/ai-bridge/pkg/shared/streamtransport"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

func buildStreamEventEnvelope(state *streamingState, part map[string]any) (turnID string, seq int, content map[string]any, ok bool) {
	turnID = strings.TrimSpace(state.turnID)
	if turnID == "" {
		return "", 0, nil, false
	}

	state.sequenceNum++
	seq = state.sequenceNum

	env, err := matrixevents.BuildStreamEventEnvelope(turnID, seq, part, matrixevents.StreamEventOpts{
		TargetEventID: state.initialEventID.String(),
		AgentID:       state.agentID,
	})
	if err != nil {
		return "", 0, nil, false
	}
	content = env

	return turnID, seq, content, true
}

// emitStreamEvent sends an AI SDK UIMessageChunk streaming event to the room (ephemeral).
func (oc *AIClient) emitStreamEvent(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	part map[string]any,
) {
	verbose := oc.logEphemeralVerbose()

	if portal == nil || portal.MXID == "" {
		if verbose {
			oc.loggerForContext(ctx).Debug().Msg("Stream event skipped: missing portal/room")
		}
		return
	}
	if state == nil {
		if verbose {
			oc.loggerForContext(ctx).Debug().Msg("Stream event skipped: missing state")
		}
		return
	}
	if state.suppressSend {
		if verbose {
			oc.loggerForContext(ctx).Debug().
				Str("turn_id", strings.TrimSpace(state.turnID)).
				Msg("Stream event suppressed (suppressSend)")
		}
		return
	}
	mode := oc.streamTransportMode()
	if mode == streamtransport.ModeDebouncedEdit {
		oc.emitDebouncedStreamPart(ctx, portal, state, part)
		return
	}
	intent := oc.getModelIntent(ctx, portal)
	if intent == nil {
		oc.loggerForContext(ctx).Warn().Msg("Stream event skipped: missing intent")
		return
	}

	ephemeralSender, ok := intent.(matrixevents.MatrixEphemeralSender)
	if !ok {
		if !state.streamEphemeralUnsupported {
			state.streamEphemeralUnsupported = true
			oc.fallbackStreamTransportToDebounced(ctx, "intent_missing_ephemeral_sender", nil)
		}
		oc.emitDebouncedStreamPart(ctx, portal, state, part)
		return
	}

	partType, _ := part["type"].(string)
	turnID, seq, content, ok := buildStreamEventEnvelope(state, part)
	if !ok {
		oc.loggerForContext(ctx).Error().
			Str("part_type", partType).
			Msg("Stream event skipped: missing turn_id")
		return
	}

	// Debounced start summary: log once per turn when verbose is off.
	if !state.loggedStreamStart {
		state.loggedStreamStart = true
		oc.loggerForContext(ctx).Info().
			Stringer("room_id", portal.MXID).
			Str("turn_id", turnID).
			Msg("Streaming ephemeral events")
	}

	eventContent := &event.Content{Raw: content}

	txnID := matrixevents.BuildStreamEventTxnID(turnID, seq)
	if verbose {
		oc.loggerForContext(ctx).Debug().
			Stringer("room_id", portal.MXID).
			Str("turn_id", turnID).
			Int("seq", seq).
			Str("part_type", partType).
			Msg("Sending stream event (ephemeral)")
	}
	_, err := ephemeralSender.SendEphemeralEvent(ctx, portal.MXID, StreamEventMessageType, eventContent, txnID)
	if err != nil {
		if streamtransport.ShouldFallbackToDebounced(err) {
			oc.fallbackStreamTransportToDebounced(ctx, "ephemeral_send_unknown", err)
			oc.emitDebouncedStreamPart(ctx, portal, state, part)
			return
		}
		oc.loggerForContext(ctx).Warn().Err(err).
			Str("part_type", partType).
			Int("seq", seq).
			Msg("Failed to emit stream event, retrying async")
		// Fire-and-forget async retry to avoid blocking the stream hot path.
		go func() {
			_, retryErr := ephemeralSender.SendEphemeralEvent(ctx, portal.MXID, StreamEventMessageType, eventContent, txnID)
			if retryErr != nil {
				oc.loggerForContext(ctx).Error().Err(retryErr).
					Str("part_type", partType).
					Int("seq", seq).
					Msg("Failed to emit stream event after async retry")
			}
		}()
	}
}
