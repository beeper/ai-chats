package connector

import (
	"context"

	"github.com/openai/openai-go/v3"
	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

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
// controller/signaler, signal run start, and apply proactive context pruning.
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
	roomID := id.RoomID("")
	if portal != nil {
		roomID = portal.MXID
	}
	state := newStreamingState(ctx, meta, sourceEventID, senderID, roomID)
	oc.setupEmitter(state)
	state.replyTarget = oc.resolveInitialReplyTarget(evt)

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

	// Apply proactive context pruning
	pruned = oc.applyProactivePruning(ctx, messages, meta)

	prep = streamingRunPrep{
		State:         state,
		TypingSignals: typingSignals,
		TouchTyping:   touchTyping,
		IsHeartbeat:   isHeartbeat,
	}
	return prep, pruned, cleanup
}
