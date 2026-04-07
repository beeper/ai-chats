package ai

import (
	"context"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote"

	bridgesdk "github.com/beeper/agentremote/sdk"
)

// createStreamingTurn builds an sdk.Turn configured with bridges/ai-specific
// hooks for initial message sending and shared stream transport delivery.
func (oc *AIClient) createStreamingTurn(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	state *streamingState,
	sourceEventID id.EventID,
	senderID string,
) *bridgesdk.Turn {
	var sdkConfig *bridgesdk.Config[*AIClient, *Config]
	if oc.connector != nil {
		sdkConfig = oc.connector.sdkConfig
	}
	var sender bridgev2.EventSender
	if oc.UserLogin != nil {
		sender = oc.senderForPortal(ctx, portal)
	}
	conv := bridgesdk.NewConversation(ctx, oc.UserLogin, portal, sender, sdkConfig, oc)
	turn := conv.StartTurn(ctx, nil, &bridgesdk.SourceRef{EventID: string(sourceEventID), SenderID: senderID})
	turn.SetSender(sender)
	turn.SetFinalMetadataProvider(bridgesdk.FinalMetadataProviderFunc(func(_ *bridgesdk.Turn, _ string) any {
		return oc.buildStreamingMessageMetadata(state, meta, nil)
	}))
	turn.Approvals().SetHandler(func(callCtx context.Context, sdkTurn *bridgesdk.Turn, req bridgesdk.ApprovalRequest) bridgesdk.ApprovalHandle {
		return oc.requestTurnApproval(callCtx, portal, state, sdkTurn, req)
	})
	placeholderExtra := map[string]any{
		BeeperAIKey: map[string]any{
			"id":   turn.ID(),
			"role": "assistant",
			"metadata": map[string]any{
				"turn_id": turn.ID(),
			},
			"parts": []any{},
		},
	}
	turn.SetPlaceholderMessagePayload(&bridgesdk.PlaceholderMessagePayload{
		Content: &event.MessageEventContent{
			MsgType:  event.MsgText,
			Body:     "...",
			Mentions: &event.Mentions{},
		},
		Extra: placeholderExtra,
		DBMetadata: &MessageMetadata{BaseMessageMetadata: agentremote.BaseMessageMetadata{
			Role:   "assistant",
			TurnID: turn.ID(),
		}},
	})

	turn.SetStreamPublisherFunc(func(callCtx context.Context) (bridgev2.BeeperStreamPublisher, bool) {
		if oc.UserLogin == nil || oc.UserLogin.Bridge == nil || oc.UserLogin.Bridge.Matrix == nil {
			return nil, false
		}
		publisher := oc.UserLogin.Bridge.GetBeeperStreamPublisher()
		if publisher == nil {
			return nil, false
		}
		return publisher, true
	})

	if state.suppressSend {
		turn.SetSuppressSend(true)
	}

	return turn
}

// streamingRunPrep holds the shared state produced by prepareStreamingRun.
type streamingRunPrep struct {
	State         *streamingState
	TypingSignals *TypingSignaler
	TouchTyping   func()
	IsHeartbeat   bool
}

// prepareStreamingRun performs the shared preamble for both the Responses API
// and Chat Completions streaming paths: initialise streaming state, set the
// reply target, ensure the model ghost is in the room, create a typing
// controller/signaler, and signal run start.
//
// The returned cleanup function MUST be deferred by the caller to mark the
// typing controller complete.
func (oc *AIClient) prepareStreamingRun(
	ctx context.Context,
	log zerolog.Logger,
	evt *event.Event,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
) (prep streamingRunPrep, cleanup func()) {
	var sourceEventID id.EventID
	senderID := ""
	if evt != nil {
		sourceEventID = evt.ID
		if evt.Sender != "" {
			senderID = evt.Sender.String()
		}
	}
	var roomID id.RoomID
	if portal != nil {
		roomID = portal.MXID
	}
	state := newStreamingState(ctx, meta, roomID)
	if responder, err := oc.ResolveResponderForMeta(ctx, meta); err == nil && responder != nil {
		state.respondingGhostID = string(responder.GhostID)
		state.respondingAgentID = responder.AgentID
		state.respondingModelID = responder.ModelID
		state.respondingContextLimit = responder.ContextLimit
	} else if err != nil {
		log.Warn().Err(err).Msg("Failed to resolve responder for streaming turn")
	}

	// Create SDK Turn for writer/emitter/session management.
	turn := oc.createStreamingTurn(ctx, portal, meta, state, sourceEventID, senderID)
	state.turn = turn
	oc.bindRoomRunState(roomID, state)

	state.replyTarget = oc.resolveInitialReplyTarget(evt)
	if state.replyTarget.ThreadRoot != "" {
		turn.SetThread(state.replyTarget.ThreadRoot)
	}
	if state.replyTarget.ReplyTo != "" {
		turn.SetReplyTo(state.replyTarget.ReplyTo)
	}

	// Ensure model ghost is in the room before any operations
	if !state.suppressSend {
		if err := oc.ensureModelInRoom(ctx, portal); err != nil {
			log.Warn().Err(err).Msg("Failed to ensure model is in room")
		}
	}

	// Create typing controller with TTL and automatic refresh
	var typingCtrl *TypingController
	var typingSignals *TypingSignaler
	touchTyping := func() {}
	isHeartbeat := state.heartbeat != nil
	if !state.suppressSend && !isHeartbeat {
		mode := oc.resolveTypingMode(meta, typingContextFromContext(ctx), isHeartbeat)
		interval := oc.resolveTypingInterval(meta)
		if interval > 0 && mode != TypingModeNever {
			typingCtrl = NewTypingController(oc, ctx, portal, TypingControllerOptions{
				Interval: interval,
				TTL:      typingTTL,
			})
			typingSignals = NewTypingSignaler(typingCtrl, mode, isHeartbeat)
			touchTyping = func() {
				typingCtrl.RefreshTTL()
			}
		}
	}
	if typingSignals != nil {
		typingSignals.SignalRunStart()
	}

	cleanup = func() {
		if typingCtrl != nil {
			typingCtrl.MarkRunComplete()
			typingCtrl.MarkDispatchIdle()
		}
	}

	prep = streamingRunPrep{
		State:         state,
		TypingSignals: typingSignals,
		TouchTyping:   touchTyping,
		IsHeartbeat:   isHeartbeat,
	}
	return prep, cleanup
}
