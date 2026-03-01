package codex

import (
	"context"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/ai-bridge/pkg/matrixevents"
	"github.com/beeper/ai-bridge/pkg/shared/streamtransport"
)

func (cc *CodexClient) sendDebouncedStreamEdit(ctx context.Context, portal *bridgev2.Portal, state *streamingState, force bool) error {
	if cc == nil || state == nil || portal == nil {
		return nil
	}
	streamtransport.SendDebouncedEdit(ctx, streamtransport.DebouncedEditParams{
		Portal:         portal,
		Force:          force,
		SuppressSend:   state.suppressSend,
		VisibleBody:    state.visibleAccumulated.String(),
		FallbackBody:   state.accumulated.String(),
		InitialEventID: state.initialEventID,
		TurnID:  state.turnID,
		Intent:  cc.getCodexIntent(ctx, portal),
		Log:            cc.loggerForContext(ctx),
	})
	return nil
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
		GetEphemeralSender: func(callCtx context.Context) (matrixevents.MatrixEphemeralSender, bool) {
			intent := cc.getCodexIntent(callCtx, portal)
			ephemeralSender, ok := intent.(matrixevents.MatrixEphemeralSender)
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
	if portal == nil || portal.MXID == "" || state == nil || state.suppressSend {
		return
	}
	if !state.loggedStreamStart {
		state.loggedStreamStart = true
		cc.loggerForContext(ctx).Info().
			Stringer("room_id", portal.MXID).
			Str("turn_id", strings.TrimSpace(state.turnID)).
			Msg("Streaming events")
	}
	session := cc.ensureStreamSession(ctx, portal, state)
	if session == nil {
		return
	}
	session.EmitPart(ctx, part)
}
