package codex

import (
	"context"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote"
	"github.com/beeper/agentremote/turns"
)

func (cc *CodexClient) sendDebouncedStreamEdit(ctx context.Context, portal *bridgev2.Portal, state *streamingState, force bool) error {
	if cc == nil || state == nil || portal == nil {
		return nil
	}
	return agentremote.SendDebouncedStreamEdit(agentremote.SendDebouncedStreamEditParams{
		Login:            cc.UserLogin,
		Portal:           portal,
		Sender:           cc.senderForPortal(),
		NetworkMessageID: state.networkMessageID,
		SuppressSend:     state.suppressSend,
		VisibleBody:      state.visibleAccumulated.String(),
		FallbackBody:     state.accumulated.String(),
		LogKey:           "codex_edit_target",
		Force:            force,
	})
}

func (cc *CodexClient) ensureStreamSession(ctx context.Context, portal *bridgev2.Portal, state *streamingState) *turns.StreamSession {
	if cc == nil || portal == nil || state == nil {
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
			return cc.resolveStreamTargetEventID(callCtx, portal, state, target)
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
		RuntimeFallbackFlag: &cc.streamFallbackToDebounced,
		GetEphemeralSender: func(callCtx context.Context) (bridgev2.EphemeralSendingMatrixAPI, bool) {
			intent, err := cc.getCodexIntentForPortal(callCtx, portal, bridgev2.RemoteEventMessage)
			if err != nil || intent == nil {
				return nil, false
			}
			ephemeralSender, ok := intent.(bridgev2.EphemeralSendingMatrixAPI)
			return ephemeralSender, ok
		},
		SendDebouncedEdit: func(callCtx context.Context, force bool) error {
			return cc.sendDebouncedStreamEdit(callCtx, portal, state, force)
		},
		SendHook: func(turnID string, seq int, content map[string]any, txnID string) bool {
			if cc.streamEventHook == nil {
				return false
			}
			cc.streamEventHook(turnID, seq, content, txnID)
			return true
		},
		Logger: cc.loggerForContext(ctx),
	})
	return state.session
}

func (cc *CodexClient) emitStreamEvent(ctx context.Context, portal *bridgev2.Portal, state *streamingState, part map[string]any) {
	if state == nil {
		return
	}
	turns.EmitStreamEvent(ctx, portal, turns.StreamEventState{
		TurnID:        state.turnID,
		SuppressSend:  state.suppressSend,
		LoggedStart:   &state.loggedStreamStart,
		EnsureSession: func() *turns.StreamSession { return cc.ensureStreamSession(ctx, portal, state) },
		Logger:        cc.loggerForContext(ctx),
	}, part)
}

func (cc *CodexClient) resolveStreamTargetEventID(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	target turns.StreamTarget,
) (id.EventID, error) {
	if state != nil && state.initialEventID != "" {
		return state.initialEventID, nil
	}
	if cc == nil || cc.UserLogin == nil || cc.UserLogin.Bridge == nil || portal == nil {
		return "", nil
	}
	receiver := portal.Receiver
	if receiver == "" {
		receiver = cc.UserLogin.ID
	}
	eventID, err := turns.ResolveTargetEventIDFromDB(ctx, cc.UserLogin.Bridge, receiver, target)
	if err == nil && eventID != "" && state != nil {
		state.initialEventID = eventID
	}
	return eventID, err
}
