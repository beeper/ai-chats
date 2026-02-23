package connector

import (
	"context"
	"errors"

	"maunium.net/go/mautrix/bridgev2"
)

func streamFailureError(state *streamingState, err error) error {
	if state != nil && state.initialEventID != "" {
		return &NonFallbackError{Err: err}
	}
	return &PreDeltaError{Err: err}
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
		state.finishReason = "cancelled"
		if state.initialEventID != "" && state.accumulated.Len() > 0 {
			oc.flushPartialStreamingMessage(context.Background(), portal, state, meta)
		}
		oc.emitUIAbort(context.Background(), portal, state, "cancelled")
		oc.emitUIFinish(context.Background(), portal, state, meta)
		return nil, streamFailureError(state, err)
	}

	if includeContextLength {
		cle := ParseContextLengthError(err)
		if cle != nil {
			return cle, nil
		}
	}

	state.finishReason = "error"
	oc.emitUIError(ctx, portal, state, err.Error())
	oc.emitUIFinish(ctx, portal, state, meta)
	return nil, streamFailureError(state, err)
}
