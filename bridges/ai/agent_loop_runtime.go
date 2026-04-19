package ai

import (
	"context"
	"errors"
	"time"

	"github.com/openai/openai-go/v3/packages/ssestream"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

const maxAgentLoopToolTurns = 50
const agentLoopInactivityTimeout = 10 * time.Minute
const heartbeatRunTimeout = 2 * time.Minute

var errAgentLoopInactivityTimeout = errors.New("agent loop inactivity timeout")

type agentLoopActivityKey struct{}

func withActivityTimeout(parent context.Context, timeout time.Duration, timeoutErr error) (context.Context, func(), context.CancelFunc) {
	ctx, cancel := context.WithCancelCause(parent)
	if timeout <= 0 {
		return ctx, func() {}, func() { cancel(context.Canceled) }
	}

	activityCh := make(chan struct{}, 1)
	go func() {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-activityCh:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(timeout)
			case <-timer.C:
				cancel(timeoutErr)
				return
			}
		}
	}()

	touch := func() {
		select {
		case activityCh <- struct{}{}:
		default:
		}
	}
	touch()
	return ctx, touch, func() { cancel(context.Canceled) }
}

func touchAgentLoopActivity(ctx context.Context) {
	if touch, ok := ctx.Value(agentLoopActivityKey{}).(func()); ok && touch != nil {
		touch()
	}
}

func agentLoopInactivityCause(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	cause := context.Cause(ctx)
	if errors.Is(cause, errAgentLoopInactivityTimeout) {
		return cause
	}
	return nil
}

func (oc *AIClient) withAgentLoopInactivityTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	runCtx, touch, cancel := withActivityTimeout(ctx, agentLoopInactivityTimeout, errAgentLoopInactivityTimeout)
	return context.WithValue(runCtx, agentLoopActivityKey{}, touch), cancel
}

func (oc *AIClient) launchAgentLoopRun(
	ctx context.Context,
	evt *event.Event,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	prompt PromptContext,
	onExit func(),
) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		if onExit != nil {
			defer onExit()
		}
		completionCtx, cancel := oc.withAgentLoopInactivityTimeout(ctx)
		defer cancel()
		oc.runAgentLoopWithRetry(completionCtx, evt, portal, meta, prompt)
	}()
	return done
}

func runAgentLoopStreamStep[T any](
	ctx context.Context,
	state *streamingState,
	stream *ssestream.Stream[T],
	handleEvent func(T) (done bool, cle *ContextLengthError, err error),
	handleErr func(error) (cle *ContextLengthError, err error),
) (bool, *ContextLengthError, error) {
	if stream != nil {
		defer stream.Close()
	}
	writer := state.writer()
	writer.StepStart(ctx)
	defer writer.StepFinish(ctx)
	touchAgentLoopActivity(ctx)
	for stream.Next() {
		touchAgentLoopActivity(ctx)
		current := stream.Current()
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
