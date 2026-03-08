package codex

import (
	"context"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/ai-bridge/pkg/bridgeadapter"
	"github.com/beeper/ai-bridge/pkg/shared/streamtransport"
)

func (cc *CodexClient) sendDebouncedStreamEdit(ctx context.Context, portal *bridgev2.Portal, state *streamingState, force bool) error {
	if cc == nil || state == nil || portal == nil {
		return nil
	}
	return bridgeadapter.SendDebouncedStreamEdit(bridgeadapter.SendDebouncedStreamEditParams{
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

func (cc *CodexClient) ensureStreamSession(ctx context.Context, portal *bridgev2.Portal, state *streamingState) *streamtransport.StreamSession {
	if cc == nil || portal == nil || state == nil {
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
	streamtransport.EmitStreamEventWithSession(
		ctx,
		portal,
		state.turnID,
		state.suppressSend,
		&state.loggedStreamStart,
		cc.loggerForContext(ctx),
		func() *streamtransport.StreamSession { return cc.ensureStreamSession(ctx, portal, state) },
		part,
	)
}
