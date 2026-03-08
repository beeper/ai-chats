package connector

import (
	"context"

	"github.com/beeper/ai-bridge/pkg/shared/streamtransport"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/id"
)

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
	if state == nil {
		return
	}
	streamtransport.EmitStreamEventWithSession(
		ctx,
		portal,
		state.turnID,
		state.suppressSend,
		&state.loggedStreamStart,
		oc.loggerForContext(ctx),
		func() *streamtransport.StreamSession { return oc.ensureStreamSession(ctx, portal, state) },
		part,
	)
}
