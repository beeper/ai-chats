package ai

import (
	"context"
	"errors"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
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

func (oc *AIClient) finishStreamingWithFailure(
	ctx context.Context,
	log zerolog.Logger,
	portal *bridgev2.Portal,
	state *streamingState,
	meta *PortalMetadata,
	reason string,
	err error,
) error {
	_ = log
	return oc.finalizeStreamingTurn(ctx, portal, state, meta, streamingFinalizeParams{
		reason: reason,
		err:    err,
	})
}

func (oc *AIClient) handleResponsesStreamErr(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	meta *PortalMetadata,
	err error,
	includeContextLength bool,
) (*ContextLengthError, error) {
	if errors.Is(err, context.Canceled) {
		if timeoutErr := agentLoopInactivityCause(ctx); timeoutErr != nil {
			return nil, oc.finishStreamingWithFailure(context.Background(), *oc.loggerForContext(ctx), portal, state, meta, "timeout", timeoutErr)
		}
		return nil, oc.finishStreamingWithFailure(context.Background(), *oc.loggerForContext(ctx), portal, state, meta, "cancelled", err)
	}

	if includeContextLength {
		cle := ParseContextLengthError(err)
		if cle != nil {
			return cle, nil
		}
	}

	return nil, oc.finishStreamingWithFailure(ctx, *oc.loggerForContext(ctx), portal, state, meta, "error", err)
}
