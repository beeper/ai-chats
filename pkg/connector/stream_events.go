package connector

import (
	"context"
	"strings"

	"github.com/beeper/ai-bridge/pkg/matrixevents"
	"github.com/beeper/ai-bridge/pkg/shared/streamtransport"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/id"
)

func buildStreamEventEnvelope(state *streamingState, part map[string]any) (turnID string, seq int, content map[string]any, ok bool) {
	if state == nil {
		return "", 0, nil, false
	}
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
	return turnID, seq, env, true
}

func (oc *AIClient) ensureStreamSession(ctx context.Context, portal *bridgev2.Portal, state *streamingState) *streamtransport.StreamSession {
	if oc == nil || portal == nil || state == nil {
		return nil
	}
	if state.session != nil {
		return state.session
	}
	state.session = streamtransport.NewStreamSession(streamtransport.StreamSessionParams{
		TurnID:  state.turnID,
		AgentID: state.agentID,
		GetTargetEventID: func() string {
			return state.initialEventID.String()
		},
		GetRoomID: func() id.RoomID {
			return portal.MXID
		},
		GetSuppressSend: func() bool {
			return state.suppressSend
		},
		NextSeq: func() int {
			state.sequenceNum++
			return state.sequenceNum
		},
		RuntimeFallbackFlag: &oc.streamFallbackToDebounced,
		GetEphemeralSender: func(callCtx context.Context) (bridgev2.EphemeralSendingMatrixAPI, bool) {
			intent, err := oc.getIntentForPortal(callCtx, portal, bridgev2.RemoteEventMessage)
			if err != nil || intent == nil {
				return nil, false
			}
			ephemeralSender, ok := intent.(bridgev2.EphemeralSendingMatrixAPI)
			return ephemeralSender, ok
		},
		SendDebouncedEdit: func(callCtx context.Context, force bool) error {
			return oc.sendDebouncedStreamEdit(callCtx, portal, state, force)
		},
		Logger: oc.loggerForContext(ctx),
	})
	return state.session
}

// emitStreamEvent routes AI SDK UIMessageChunk parts through shared stream transport.
// Transport attempts ephemeral delivery first and automatically falls back to
// debounced timeline edits when ephemeral streaming is unavailable.
func (oc *AIClient) emitStreamEvent(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	part map[string]any,
) {
	if portal == nil || portal.MXID == "" || state == nil || state.suppressSend {
		return
	}
	if !state.loggedStreamStart {
		state.loggedStreamStart = true
		oc.loggerForContext(ctx).Info().
			Stringer("room_id", portal.MXID).
			Str("turn_id", strings.TrimSpace(state.turnID)).
			Msg("Streaming events")
	}
	session := oc.ensureStreamSession(ctx, portal, state)
	if session == nil {
		return
	}
	session.EmitPart(ctx, part)
}
