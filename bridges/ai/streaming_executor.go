package ai

import (
	"context"

	"github.com/openai/openai-go/v3"
	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

// streamingTurnAdapter owns provider-specific request construction and stream parsing
// while the executor owns the shared turn lifecycle.
type streamingTurnAdapter interface {
	TrackRoomRunStreaming() bool
	RunRound(ctx context.Context, evt *event.Event, round int) (continueLoop bool, cle *ContextLengthError, err error)
	Finalize(ctx context.Context)
}

type streamingAdapterBase struct {
	oc            *AIClient
	log           zerolog.Logger
	portal        *bridgev2.Portal
	meta          *PortalMetadata
	state         *streamingState
	typingSignals *TypingSignaler
	touchTyping   func()
	isHeartbeat   bool
	messages      []openai.ChatCompletionMessageParamUnion
}

func newStreamingAdapterBase(
	oc *AIClient,
	log zerolog.Logger,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	prep streamingRunPrep,
	messages []openai.ChatCompletionMessageParamUnion,
) streamingAdapterBase {
	return streamingAdapterBase{
		oc:            oc,
		log:           log,
		portal:        portal,
		meta:          meta,
		state:         prep.State,
		typingSignals: prep.TypingSignals,
		touchTyping:   prep.TouchTyping,
		isHeartbeat:   prep.IsHeartbeat,
		messages:      messages,
	}
}

func (oc *AIClient) runStreamingTurn(
	ctx context.Context,
	log zerolog.Logger,
	evt *event.Event,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	messages []openai.ChatCompletionMessageParamUnion,
	newAdapter func(prep streamingRunPrep, pruned []openai.ChatCompletionMessageParamUnion) streamingTurnAdapter,
) (bool, *ContextLengthError, error) {
	prep, pruned, typingCleanup := oc.prepareStreamingRun(ctx, log, evt, portal, meta, messages)
	defer typingCleanup()

	state := prep.State
	adapter := newAdapter(prep, pruned)
	if state.roomID != "" {
		if adapter.TrackRoomRunStreaming() {
			oc.markRoomRunStreaming(state.roomID, true)
			defer oc.markRoomRunStreaming(state.roomID, false)
		}
	}

	oc.emitUIStart(ctx, portal, state, meta)
	for round := 0; ; round++ {
		continueLoop, cle, err := adapter.RunRound(ctx, evt, round)
		if cle != nil || err != nil {
			return false, cle, err
		}
		if !continueLoop {
			adapter.Finalize(ctx)
			return true, nil, nil
		}
	}
}
