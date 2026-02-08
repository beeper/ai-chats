package connector

import (
	"context"
	"strings"
	"time"

	"github.com/beeper/ai-bridge/pkg/matrixevents"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

type matrixEphemeralSender interface {
	SendEphemeralEvent(ctx context.Context, roomID id.RoomID, eventType event.Type, content *event.Content, txnID string) (*mautrix.RespSendEvent, error)
}

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
	intent := oc.getModelIntent(ctx, portal)
	if intent == nil {
		oc.loggerForContext(ctx).Warn().Msg("Stream event skipped: missing intent")
		return
	}

	ephemeralSender, ok := intent.(matrixEphemeralSender)
	if !ok {
		if !state.streamEphemeralUnsupported {
			state.streamEphemeralUnsupported = true
			partType, _ := part["type"].(string)
			oc.loggerForContext(ctx).Warn().
				Str("part_type", partType).
				Msg("Matrix intent does not support ephemeral events; stream updates will be dropped")
		}
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

	txnID := buildStreamEventTxnID(turnID, seq)
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
		oc.loggerForContext(ctx).Warn().Err(err).
			Str("part_type", partType).
			Int("seq", seq).
			Msg("Failed to emit stream event, retrying once")
		time.Sleep(100 * time.Millisecond)
		_, err = ephemeralSender.SendEphemeralEvent(ctx, portal.MXID, StreamEventMessageType, eventContent, txnID)
		if err != nil {
			oc.loggerForContext(ctx).Error().Err(err).
				Str("part_type", partType).
				Int("seq", seq).
				Msg("Failed to emit stream event after retry")
		}
	}
}

func buildStreamEventTxnID(turnID string, seq int) string {
	return matrixevents.BuildStreamEventTxnID(turnID, seq)
}
