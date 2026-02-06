package connector

import (
	"context"
	"fmt"
	"strings"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

type matrixEphemeralSender interface {
	SendEphemeralEvent(ctx context.Context, roomID id.RoomID, eventType event.Type, content *event.Content, txnID string) (*mautrix.RespSendEvent, error)
}

// matrixEphemeralSenderLegacy supports older/local intents that expose SendEphemeral
// instead of SendEphemeralEvent.
type matrixEphemeralSenderLegacy interface {
	SendEphemeral(ctx context.Context, roomID id.RoomID, eventType event.Type, content *event.Content, txnID string) (*mautrix.RespSendEvent, error)
}

func buildStreamEventEnvelope(state *streamingState, part map[string]any) (turnID string, seq int, content map[string]any, ok bool) {
	turnID = strings.TrimSpace(state.turnID)
	if turnID == "" {
		return "", 0, nil, false
	}

	state.sequenceNum++
	seq = state.sequenceNum

	// Conformance invariants:
	// - turn_id is required and non-empty.
	// - seq is strictly monotonic per turn (state.sequenceNum++).
	// - part is the AI SDK chunk payload passed through unchanged.
	content = map[string]any{
		"turn_id": turnID,
		"seq":     seq,
		"part":    part,
	}
	if state.initialEventID != "" {
		content["target_event"] = state.initialEventID.String()
		content["m.relates_to"] = map[string]any{
			"rel_type": RelReference,
			"event_id": state.initialEventID.String(),
		}
	}
	if state.agentID != "" {
		content["agent_id"] = state.agentID
	}

	return turnID, seq, content, true
}

// emitStreamEvent sends an AI SDK UIMessageChunk streaming event to the room (ephemeral).
func (oc *AIClient) emitStreamEvent(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	part map[string]any,
) {
	if portal == nil || portal.MXID == "" {
		oc.log.Debug().Msg("Stream event skipped: missing portal/room")
		return
	}
	if state == nil {
		oc.log.Debug().Msg("Stream event skipped: missing state")
		return
	}
	if state != nil && state.suppressSend {
		oc.log.Debug().
			Str("turn_id", strings.TrimSpace(state.turnID)).
			Msg("Stream event suppressed (suppressSend)")
		return
	}
	intent := oc.getModelIntent(ctx, portal)
	if intent == nil {
		oc.log.Warn().Msg("Stream event skipped: missing intent")
		return
	}

	ephemeralSender, ok := intent.(matrixEphemeralSender)
	legacySender, legacyOK := intent.(matrixEphemeralSenderLegacy)
	if !ok && !legacyOK {
		if !state.streamEphemeralUnsupported {
			state.streamEphemeralUnsupported = true
			partType, _ := part["type"].(string)
			oc.log.Warn().
				Str("part_type", partType).
				Msg("Matrix intent does not support ephemeral events; stream updates will be dropped")
		}
		return
	}

	partType, _ := part["type"].(string)
	turnID, seq, content, ok := buildStreamEventEnvelope(state, part)
	if !ok {
		oc.log.Error().
			Str("part_type", partType).
			Msg("Stream event skipped: missing turn_id")
		return
	}

	eventContent := &event.Content{Raw: content}

	txnID := buildStreamEventTxnID(turnID, seq)
	oc.log.Debug().
		Stringer("room_id", portal.MXID).
		Str("turn_id", turnID).
		Int("seq", seq).
		Str("part_type", partType).
		Bool("legacy", !ok && legacyOK).
		Msg("Sending stream event (ephemeral)")

	var err error
	if ok {
		_, err = ephemeralSender.SendEphemeralEvent(ctx, portal.MXID, StreamEventMessageType, eventContent, txnID)
	} else {
		_, err = legacySender.SendEphemeral(ctx, portal.MXID, StreamEventMessageType, eventContent, txnID)
	}
	if err != nil {
		oc.log.Warn().Err(err).
			Str("part_type", partType).
			Int("seq", seq).
			Msg("Failed to emit stream event")
	}
}

func buildStreamEventTxnID(turnID string, seq int) string {
	if turnID == "" {
		return fmt.Sprintf("ai_stream_%d", seq)
	}
	return fmt.Sprintf("ai_stream_%s_%d", turnID, seq)
}
