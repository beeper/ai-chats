package ai

import (
	"context"
	"time"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote/sdk"
)

type streamingFinalizeParams struct {
	reason                string
	err                   error
	success               bool
	finalizeAccumulator   bool
	recordProviderSuccess bool
	generateTitle         bool
}

func (oc *AIClient) finalizeStreamingTurn(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	meta *PortalMetadata,
	params streamingFinalizeParams,
) error {
	if state == nil {
		return params.err
	}
	if !state.markFinalized() {
		if params.success {
			return nil
		}
		return streamFailureError(state, params.err)
	}

	reason := params.reason
	if !params.success && state.stop.Load() != nil && reason == "cancelled" {
		reason = "stop"
	}
	state.completedAtMs = time.Now().UnixMilli()
	if params.success {
		if state.finishReason == "" {
			state.finishReason = "stop"
		}
		reason = state.finishReason
		if state.responseStatus == "" && state.responseID != "" {
			state.responseStatus = canonicalResponseStatus(state)
		}
		if params.finalizeAccumulator && oc != nil && state.replyAccumulator != nil {
			if parsed := state.replyAccumulator.Consume("", true); parsed != nil {
				oc.applyStreamingReplyTarget(state, parsed)
			}
		}
	} else {
		state.finishReason = reason
	}

	if state.hasInitialMessageTarget() || state.heartbeat != nil {
		oc.sendFinalAssistantTurn(ctx, portal, state, meta)
	}
	if writer := state.writer(); writer != nil {
		writer.MessageMetadata(ctx, oc.buildUIMessageMetadata(state, meta, true))
		if !params.success && reason == "cancelled" {
			writer.Abort(ctx, "cancelled")
		}
	}
	if state.turn != nil {
		switch {
		case params.success:
			state.turn.End(sdk.MapFinishReason(reason))
		case reason == "cancelled":
			state.turn.End("cancelled")
		case reason == "stop":
			state.turn.End(sdk.MapFinishReason(reason))
		default:
			errText := "streaming failed"
			if params.err != nil {
				errText = params.err.Error()
			}
			state.turn.EndWithError(errText)
		}
	}
	oc.noteStreamingPersistenceSideEffects(ctx, portal, state, meta)
	if params.success {
		if params.generateTitle {
			oc.maybeGenerateTitle(ctx, portal, finalRenderedBodyFallback(state))
		}
		if params.recordProviderSuccess {
			oc.recordProviderSuccess(ctx)
		}
		return nil
	}
	return streamFailureError(state, params.err)
}
