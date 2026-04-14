package ai

import (
	"context"
	"errors"
)

// NonFallbackError marks an error as ineligible for fallback retries once output has been sent.
type NonFallbackError struct {
	Err error
}

func (e *NonFallbackError) Error() string {
	return e.Err.Error()
}

func (e *NonFallbackError) Unwrap() error {
	return e.Err
}

func streamFailureError(state *streamingState, err error) error {
	if state != nil && state.hasInitialMessageTarget() {
		return &NonFallbackError{Err: err}
	}
	return &PreDeltaError{Err: err}
}

func resolveStreamingTerminalError(
	ctx context.Context,
	includeContextLength bool,
	cancelFinalizeCtx context.Context,
	err error,
) (finalizeCtx context.Context, reason string, cle *ContextLengthError, finalErr error) {
	if errors.Is(err, context.Canceled) {
		if timeoutErr := agentLoopInactivityCause(ctx); timeoutErr != nil {
			return cancelFinalizeCtx, "timeout", nil, timeoutErr
		}
		return cancelFinalizeCtx, "cancelled", nil, err
	}
	if includeContextLength {
		if cle := ParseContextLengthError(err); cle != nil {
			return ctx, "context-length", cle, err
		}
	}
	return nil, "", nil, nil
}
