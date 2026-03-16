package ai

import (
	"context"

	"github.com/openai/openai-go/v3"
	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote"
	bridgesdk "github.com/beeper/agentremote/sdk"
)

// createStreamingTurn builds an sdk.Turn configured with bridges/ai-specific
// hooks for initial message sending, ephemeral delivery, and debounced edits.
func (oc *AIClient) createStreamingTurn(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	state *streamingState,
	sourceEventID id.EventID,
	senderID string,
) *bridgesdk.Turn {
	var sdkConfig *bridgesdk.Config
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
	// Use bridges/ai's own initial message sending.
	turn.SetSendFunc(func(sendCtx context.Context) (id.EventID, networkid.MessageID, error) {
		if !state.suppressSend {
			oc.ensureGhostDisplayName(sendCtx, oc.effectiveModel(meta))
		}
		evtID, msgID := oc.sendInitialStreamMessage(sendCtx, portal, "...", turn.ID(), state.replyTarget, state.nextMessageTiming())
		return evtID, msgID, nil
	})

	// Use model-specific intent for ephemeral streaming delivery.
	turn.SetEphemeralSenderFunc(func(callCtx context.Context) (bridgev2.EphemeralSendingMatrixAPI, bool) {
		intent, err := oc.getIntentForPortal(callCtx, portal, bridgev2.RemoteEventMessage)
		if err != nil || intent == nil {
			return nil, false
		}
		ephemeralSender, ok := intent.(bridgev2.EphemeralSendingMatrixAPI)
		return ephemeralSender, ok
	})

	// Use bridges/ai's debounced edit with directive-processed visible text.
	turn.SetDebouncedEditFunc(func(callCtx context.Context, force bool) error {
		return agentremote.SendDebouncedStreamEdit(agentremote.SendDebouncedStreamEditParams{
			Login:            oc.UserLogin,
			Portal:           portal,
			Sender:           oc.senderForPortal(callCtx, portal),
			NetworkMessageID: turn.NetworkMessageID(),
			SuppressSend:     state.suppressSend,
			VisibleBody:      visibleStreamingText(state),
			FallbackBody:     state.accumulated.String(),
			LogKey:           "ai_edit_target",
			Force:            force,
			UIMessage:        oc.buildStreamUIMessage(state, nil, nil),
		})
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
	messages []openai.ChatCompletionMessageParamUnion,
) (prep streamingRunPrep, pruned []openai.ChatCompletionMessageParamUnion, cleanup func()) {
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

	// Create SDK Turn for writer/emitter/session management.
	turn := oc.createStreamingTurn(ctx, portal, meta, state, sourceEventID, senderID)
	state.turn = turn

	state.replyTarget = oc.resolveInitialReplyTarget(evt)
	if isSimpleMode(meta) {
		state.replyTarget = ReplyTarget{}
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

	pruned = messages

	prep = streamingRunPrep{
		State:         state,
		TypingSignals: typingSignals,
		TouchTyping:   touchTyping,
		IsHeartbeat:   isHeartbeat,
	}
	return prep, pruned, cleanup
}
