package ai

import (
	"context"

	"github.com/beeper/agentremote/turns"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/id"
)

func (oc *AIClient) ensureStreamSession(ctx context.Context, portal *bridgev2.Portal, state *streamingState) *turns.StreamSession {
	if oc == nil || portal == nil || state == nil {
		return nil
	}
	if state.session != nil {
		return state.session
	}
	state.session = turns.NewStreamSession(turns.StreamSessionParams{
		TurnID:  state.turnID,
		AgentID: state.agentID,
		GetStreamTarget: func() turns.StreamTarget {
			return state.streamTarget()
		},
		ResolveTargetEventID: func(callCtx context.Context, target turns.StreamTarget) (id.EventID, error) {
			return oc.resolveStreamTargetEventID(callCtx, portal, state, target)
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
	if state == nil {
		return
	}
	turns.EmitStreamEvent(ctx, portal, turns.StreamEventState{
		TurnID:        state.turnID,
		SuppressSend:  state.suppressSend,
		LoggedStart:   &state.loggedStreamStart,
		EnsureSession: func() *turns.StreamSession { return oc.ensureStreamSession(ctx, portal, state) },
		Logger:        oc.loggerForContext(ctx),
	}, part)
}

func (oc *AIClient) resolveStreamTargetEventID(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	target turns.StreamTarget,
) (id.EventID, error) {
	if state != nil && state.initialEventID != "" {
		return state.initialEventID, nil
	}
	if oc == nil || oc.UserLogin == nil || oc.UserLogin.Bridge == nil || portal == nil {
		return "", nil
	}
	receiver := portal.Receiver
	if receiver == "" {
		receiver = oc.UserLogin.ID
	}
	eventID, err := turns.ResolveTargetEventIDFromDB(ctx, oc.UserLogin.Bridge, receiver, target)
	if err == nil && eventID != "" && state != nil {
		state.initialEventID = eventID
	}
	return eventID, err
}
