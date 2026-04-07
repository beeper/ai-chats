package ai

import (
	"context"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

// agentLoopProvider owns provider-specific request construction and stream parsing
// while the agent loop owns the shared turn lifecycle.
type agentLoopProvider interface {
	TrackRoomRunStreaming() bool
	RunAgentTurn(ctx context.Context, evt *event.Event, round int) (continueLoop bool, cle *ContextLengthError, err error)
	FinalizeAgentLoop(ctx context.Context)
}

type agentLoopProviderBase struct {
	oc            *AIClient
	log           zerolog.Logger
	portal        *bridgev2.Portal
	meta          *PortalMetadata
	state         *streamingState
	typingSignals *TypingSignaler
	touchTyping   func()
	isHeartbeat   bool
	prompt        PromptContext
}

func newAgentLoopProviderBase(
	oc *AIClient,
	log zerolog.Logger,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	prep streamingRunPrep,
	prompt PromptContext,
) agentLoopProviderBase {
	return agentLoopProviderBase{
		oc:            oc,
		log:           log,
		portal:        portal,
		meta:          meta,
		state:         prep.State,
		typingSignals: prep.TypingSignals,
		touchTyping:   prep.TouchTyping,
		isHeartbeat:   prep.IsHeartbeat,
		prompt:        prompt,
	}
}

func (oc *AIClient) runAgentLoop(
	ctx context.Context,
	log zerolog.Logger,
	evt *event.Event,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	prompt PromptContext,
	newProvider func(prep streamingRunPrep, prompt PromptContext) agentLoopProvider,
) (bool, *ContextLengthError, error) {
	prep, typingCleanup := oc.prepareStreamingRun(ctx, log, evt, portal, meta)
	defer typingCleanup()

	state := prep.State
	provider := newProvider(prep, prompt)
	if state.roomID != "" {
		if provider.TrackRoomRunStreaming() {
			oc.markRoomRunStreaming(state.roomID, true)
			defer oc.markRoomRunStreaming(state.roomID, false)
		}
	}

	state.writer().Start(ctx, oc.buildUIMessageMetadata(state, meta, false))
	return executeAgentLoopRounds(ctx, provider, evt)
}

func executeAgentLoopRounds(
	ctx context.Context,
	provider agentLoopProvider,
	evt *event.Event,
) (bool, *ContextLengthError, error) {
	for round := 0; ; round++ {
		touchAgentLoopActivity(ctx)
		continueLoop, cle, err := provider.RunAgentTurn(ctx, evt, round)
		touchAgentLoopActivity(ctx)
		if cle != nil || err != nil {
			finalizeAgentLoopExit(ctx, provider)
			return false, cle, err
		}
		if continueLoop {
			continue
		}

		// Queued user messages are dispatched after room release via processPendingQueue.
		// Finalize this turn immediately so later prompts cannot reopen it with more edits.
		finalizeAgentLoopExit(ctx, provider)
		return true, nil, nil
	}
}

func finalizeAgentLoopExit(ctx context.Context, provider agentLoopProvider) {
	if provider == nil {
		return
	}
	provider.FinalizeAgentLoop(ctx)
}
