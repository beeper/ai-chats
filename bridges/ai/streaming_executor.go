package ai

import (
	"context"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

// streamProvider owns provider-specific request construction and stream parsing
// while the streaming loop owns the shared turn lifecycle.
type streamProvider interface {
	TrackRoomRunStreaming() bool
	RunStreamingTurn(ctx context.Context, evt *event.Event, round int) (continueLoop bool, cle *ContextLengthError, err error)
	FinalizeStreamingTurn(ctx context.Context)
}

type streamProviderBase struct {
	oc            *AIClient
	log           zerolog.Logger
	portal        *bridgev2.Portal
	meta          *PortalMetadata
	state         *streamingState
	typingSignals *TypingSignaler
	touchTyping   func()
	prompt        PromptContext
}

func newStreamProviderBase(
	oc *AIClient,
	log zerolog.Logger,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	prep streamingRunPrep,
	prompt PromptContext,
) streamProviderBase {
	return streamProviderBase{
		oc:            oc,
		log:           log,
		portal:        portal,
		meta:          meta,
		state:         prep.State,
		typingSignals: prep.TypingSignals,
		touchTyping:   prep.TouchTyping,
		prompt:        prompt,
	}
}

func (oc *AIClient) runStreamingLoop(
	ctx context.Context,
	log zerolog.Logger,
	evt *event.Event,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	prompt PromptContext,
	newProvider func(prep streamingRunPrep, prompt PromptContext) streamProvider,
) (bool, *ContextLengthError, error) {
	prep, typingCleanup := oc.prepareStreamingRun(ctx, log, evt, portal, meta)
	defer typingCleanup()

	state := prep.State
	if state != nil && state.currentUserMessage == "" {
		state.currentUserMessage = promptCurrentUserVisibleText(prompt)
	}
	provider := newProvider(prep, prompt)
	if state.roomID != "" {
		if provider.TrackRoomRunStreaming() {
			oc.markRoomRunStreaming(state.roomID, true)
			defer oc.markRoomRunStreaming(state.roomID, false)
		}
	}

	return executeStreamingRounds(ctx, provider, evt)
}

func executeStreamingRounds(
	ctx context.Context,
	provider streamProvider,
	evt *event.Event,
) (bool, *ContextLengthError, error) {
	for round := 0; ; round++ {
		touchStreamingActivity(ctx)
		continueLoop, cle, err := provider.RunStreamingTurn(ctx, evt, round)
		touchStreamingActivity(ctx)
		if cle != nil || err != nil {
			finalizeStreamingExit(ctx, provider)
			return false, cle, err
		}
		if continueLoop {
			continue
		}

		// Queued user messages are dispatched after room release via processPendingQueue.
		// Finalize this turn immediately so later prompts cannot reopen it with more edits.
		finalizeStreamingExit(ctx, provider)
		return true, nil, nil
	}
}

func finalizeStreamingExit(ctx context.Context, provider streamProvider) {
	if provider == nil {
		return
	}
	provider.FinalizeStreamingTurn(ctx)
}
