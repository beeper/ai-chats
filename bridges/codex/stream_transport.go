package codex

import (
	"context"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/ai-bridge/pkg/shared/streamtransport"
)

func (cc *CodexClient) sendDebouncedStreamEdit(ctx context.Context, portal *bridgev2.Portal, state *streamingState, force bool) error {
	if cc == nil || state == nil || portal == nil {
		return nil
	}
	content := streamtransport.BuildDebouncedEditContent(streamtransport.DebouncedEditParams{
		PortalMXID:   portal.MXID.String(),
		Force:        force,
		SuppressSend: state.suppressSend,
		VisibleBody:  state.visibleAccumulated.String(),
		FallbackBody: state.accumulated.String(),
	})
	if content == nil || state.networkMessageID == "" {
		return nil
	}
	sender := cc.senderForPortal()
	cc.UserLogin.QueueRemoteEvent(&CodexRemoteEdit{
		Portal:        portal.PortalKey,
		Sender:        sender,
		TargetMessage: state.networkMessageID,
		Timestamp:     time.Now(),
		LogKey:        "codex_edit_target",
		PreBuilt: &bridgev2.ConvertedEdit{
			ModifiedParts: []*bridgev2.ConvertedEditPart{{
				Type: event.EventMessage,
				Content: &event.MessageEventContent{
					MsgType:       event.MsgText,
					Body:          content.Body,
					Format:        content.Format,
					FormattedBody: content.FormattedBody,
				},
				Extra: map[string]any{"m.mentions": map[string]any{}},
				TopLevelExtra: map[string]any{
					"com.beeper.dont_render_edited": true,
					"m.mentions":                    map[string]any{},
				},
			}},
		},
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
