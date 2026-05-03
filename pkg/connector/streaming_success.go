package connector

import (
	"context"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/format"

	airuntime "github.com/beeper/ai-chats/pkg/runtime"
	"github.com/beeper/ai-chats/pkg/shared/aihelpers"
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
	if params.success {
		reason = state.finalizeTerminalSuccess()
		if params.finalizeAccumulator && oc != nil && state.replyAccumulator != nil {
			if parsed := state.replyAccumulator.Consume("", true); parsed != nil {
				oc.applyStreamingReplyTarget(state, parsed)
			}
		}
	} else {
		state.setTerminalFailure(reason)
		reason = state.finishReason
	}

	if state.hasInitialMessageTarget() && !state.suppressSend {
		rawContent := state.accumulated.String()
		directives := airuntime.ParseReplyDirectives(rawContent, state.sourceEventID().String())
		if directives.IsSilent {
			oc.loggerForContext(ctx).Debug().
				Str("turn_id", state.turn.ID()).
				Str("initial_event_id", state.turn.InitialEventID().String()).
				Msg("Silent reply detected, redacting streaming message")
			oc.redactInitialStreamingMessage(ctx, portal, state)
		} else {
			cleanedContent := airuntime.SanitizeChatMessageForDisplay(directives.Text, false)
			if strings.TrimSpace(cleanedContent) == "" {
				cleanedContent = finalRenderedBodyFallback(state)
			}
			finalReplyTarget := oc.resolveFinalReplyTarget(meta, state, &directives)
			rendered := format.RenderMarkdown(cleanedContent, true, true)
			oc.sendFinalAssistantTurnContent(ctx, portal, state, meta, cleanedContent, rendered, finalReplyTarget, "natural")
		}
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
			state.turn.End(aihelpers.MapFinishReason(reason))
		case reason == "cancelled":
			state.turn.End("cancelled")
		case reason == "stop":
			state.turn.End(aihelpers.MapFinishReason(reason))
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
			oc.maybeGenerateTitle(ctx, portal, state.currentUserMessage, finalRenderedBodyFallback(state))
		}
		if params.recordProviderSuccess {
			oc.recordProviderSuccess(ctx)
		}
		return nil
	}
	return streamFailureError(state, params.err)
}
