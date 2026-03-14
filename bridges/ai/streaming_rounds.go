package ai

import (
	"context"

	"github.com/openai/openai-go/v3/packages/ssestream"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

const maxStreamingToolRounds = 10

func hasPendingStreamingContinuation(state *streamingState) bool {
	return state != nil && (len(state.pendingFunctionOutputs) > 0 || len(state.pendingMcpApprovals) > 0)
}

func runStreamingStep[T any](
	ctx context.Context,
	oc *AIClient,
	portal *bridgev2.Portal,
	state *streamingState,
	evt *event.Event,
	stream *ssestream.Stream[T],
	shouldMarkSuccess func(T) bool,
	handleEvent func(T) (done bool, cle *ContextLengthError, err error),
	handleErr func(error) (cle *ContextLengthError, err error),
) (bool, *ContextLengthError, error) {
	oc.uiEmitter(state).EmitUIStepStart(ctx, portal)
	defer oc.uiEmitter(state).EmitUIStepFinish(ctx, portal)
	for stream.Next() {
		current := stream.Current()
		if shouldMarkSuccess == nil || shouldMarkSuccess(current) {
			oc.markMessageSendSuccess(ctx, portal, evt, state)
		}
		done, cle, err := handleEvent(current)
		if done || cle != nil || err != nil {
			return done, cle, err
		}
	}
	if err := stream.Err(); err != nil {
		cle, handledErr := handleErr(err)
		if cle != nil || handledErr != nil {
			return false, cle, handledErr
		}
	}
	return false, nil, nil
}
