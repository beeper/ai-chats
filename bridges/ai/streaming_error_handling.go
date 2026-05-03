package ai

import (
	"context"
	"errors"

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

func resolveStreamingTerminalError(
	ctx context.Context,
	includeContextLength bool,
	cancelFinalizeCtx context.Context,
	err error,
) (finalizeCtx context.Context, reason string, cle *ContextLengthError, finalErr error) {
	if errors.Is(err, context.Canceled) {
		return cancelFinalizeCtx, "cancelled", nil, err
	}
	if includeContextLength {
		if cle := ParseContextLengthError(err); cle != nil {
			return ctx, "context-length", cle, err
		}
	}
	return nil, "", nil, nil
}

func (oc *AIClient) finalizeStreamingStepError(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	meta *PortalMetadata,
	includeContextLength bool,
	cancelFinalizeCtx context.Context,
	stepErr error,
	logUnhandled func(error),
) (*ContextLengthError, error) {
	finalizeCtx, reason, cle, finalErr := resolveStreamingTerminalError(ctx, includeContextLength, cancelFinalizeCtx, stepErr)
	if reason != "" {
		err := oc.finalizeStreamingTurn(finalizeCtx, portal, state, meta, streamingFinalizeParams{
			reason: reason,
			err:    finalErr,
		})
		if cle != nil {
			if err != nil {
				oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to finalize context-length streaming turn")
			}
			return cle, nil
		}
		return nil, err
	}
	if logUnhandled != nil {
		logUnhandled(stepErr)
	}
	return nil, oc.finalizeStreamingTurn(ctx, portal, state, meta, streamingFinalizeParams{
		reason: "error",
		err:    stepErr,
	})
}
