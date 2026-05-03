package aihelpers

import (
	"context"
	"time"

	"maunium.net/go/mautrix/bridgev2"
)

type DisplayAndWaitLoopResult struct {
	Step     *bridgev2.LoginStep
	Continue bool
}

type DisplayAndWaitLoopConfig[Start any, Completion any] struct {
	Deadline           time.Time
	PollInterval       time.Duration
	ReturnAfter        time.Duration
	StartSignal        <-chan Start
	OnStartSignal      func(context.Context, Start) (*DisplayAndWaitLoopResult, error)
	CompletionSignal   <-chan Completion
	OnCompletionSignal func(context.Context, Completion) (*DisplayAndWaitLoopResult, error)
	OnPoll             func(context.Context) (*DisplayAndWaitLoopResult, error)
	ReturnStep         func() *bridgev2.LoginStep
	ContextDoneStep    func() *bridgev2.LoginStep
	OnTimeout          func() error
}

func RunDisplayAndWaitLoop[Start any, Completion any](ctx context.Context, cfg DisplayAndWaitLoopConfig[Start, Completion]) (*bridgev2.LoginStep, error) {
	if cfg.Deadline.IsZero() {
		if cfg.OnTimeout != nil {
			return nil, cfg.OnTimeout()
		}
		return nil, context.DeadlineExceeded
	}
	remaining := time.Until(cfg.Deadline)
	if remaining <= 0 {
		if cfg.OnTimeout != nil {
			return nil, cfg.OnTimeout()
		}
		return nil, context.DeadlineExceeded
	}

	deadline := time.NewTimer(remaining)
	defer deadline.Stop()

	var tick *time.Ticker
	if cfg.PollInterval > 0 {
		tick = time.NewTicker(cfg.PollInterval)
		defer tick.Stop()
	}

	var returnAfter *time.Timer
	if cfg.ReturnAfter > 0 {
		returnAfter = time.NewTimer(cfg.ReturnAfter)
		defer returnAfter.Stop()
	}

	startCh := cfg.StartSignal
	completionCh := cfg.CompletionSignal

	for {
		select {
		case value, ok := <-startCh:
			startCh = nil
			if !ok || cfg.OnStartSignal == nil {
				continue
			}
			result, err := cfg.OnStartSignal(ctx, value)
			if err != nil {
				return nil, err
			}
			if result == nil || result.Continue {
				continue
			}
			return result.Step, nil
		case value, ok := <-completionCh:
			if !ok {
				completionCh = nil
				continue
			}
			if cfg.OnCompletionSignal == nil {
				continue
			}
			result, err := cfg.OnCompletionSignal(ctx, value)
			if err != nil {
				return nil, err
			}
			if result == nil || result.Continue {
				continue
			}
			return result.Step, nil
		case <-tickChan(tick):
			if cfg.OnPoll == nil {
				continue
			}
			result, err := cfg.OnPoll(ctx)
			if err != nil {
				return nil, err
			}
			if result == nil || result.Continue {
				continue
			}
			return result.Step, nil
		case <-timerChan(returnAfter):
			if cfg.ReturnStep != nil {
				return cfg.ReturnStep(), nil
			}
			return nil, nil
		case <-deadline.C:
			if cfg.OnTimeout != nil {
				return nil, cfg.OnTimeout()
			}
			return nil, context.DeadlineExceeded
		case <-ctx.Done():
			if cfg.ContextDoneStep != nil {
				return cfg.ContextDoneStep(), nil
			}
			return nil, ctx.Err()
		}
	}
}

func tickChan(tick *time.Ticker) <-chan time.Time {
	if tick == nil {
		return nil
	}
	return tick.C
}

func timerChan(timer *time.Timer) <-chan time.Time {
	if timer == nil {
		return nil
	}
	return timer.C
}
