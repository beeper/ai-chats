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
	GetFollowUpMessages(ctx context.Context) []PromptMessage
	ContinueAgentLoop(messages []PromptMessage)
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

func (a *agentLoopProviderBase) GetFollowUpMessages(context.Context) []PromptMessage {
	if a == nil || a.oc == nil || a.state == nil {
		return nil
	}
	return a.oc.getFollowUpMessages(a.state.roomID)
}

func (a *agentLoopProviderBase) ContinueAgentLoop(messages []PromptMessage) {
	if a == nil || len(messages) == 0 {
		return
	}
	a.prompt.Messages = append(a.prompt.Messages, messages...)
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
	messages := PromptContextToChatCompletionMessages(prompt, oc.isOpenRouterProvider())
	prep, _, typingCleanup := oc.prepareStreamingRun(ctx, log, evt, portal, meta, messages)
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
		continueLoop, cle, err := provider.RunAgentTurn(ctx, evt, round)
		if cle != nil || err != nil {
			finalizeAgentLoopExit(ctx, provider, true)
			return false, cle, err
		}
		if continueLoop {
			continue
		}

		followUpMessages := provider.GetFollowUpMessages(ctx)
		if len(followUpMessages) > 0 {
			provider.ContinueAgentLoop(followUpMessages)
			continue
		}

		finalizeAgentLoopExit(ctx, provider, false)
		return true, nil, nil
	}
}

func finalizeAgentLoopExit(ctx context.Context, provider agentLoopProvider, errorExit bool) {
	if provider == nil {
		return
	}
	if errorExit {
		switch p := provider.(type) {
		case *chatCompletionsTurnAdapter:
			if p != nil && p.state != nil && p.state.completedAtMs != 0 {
				return
			}
		case *responsesTurnAdapter:
			if p != nil && p.state != nil && p.state.completedAtMs != 0 {
				return
			}
		}
	}
	provider.FinalizeAgentLoop(ctx)
}
