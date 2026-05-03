package ai

import (
	"context"
	"errors"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

type responseFuncCanonical func(ctx context.Context, evt *event.Event, portal *bridgev2.Portal, meta *PortalMetadata, prompt PromptContext) (bool, *ContextLengthError, error)

func (oc *AIClient) responseWithRetry(
	ctx context.Context,
	evt *event.Event,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	prompt PromptContext,
	responseFn responseFuncCanonical,
	logLabel string,
) (bool, error) {
	success, cle, err := responseFn(ctx, evt, portal, meta, prompt)
	if success || err == nil && cle == nil {
		return success, nil
	}
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return true, nil
		}
		oc.loggerForContext(ctx).Warn().Err(err).Str("log_label", logLabel).Msg("Response failed")
		return false, err
	}
	oc.notifyContextLengthExceeded(ctx, portal, cle, false)
	return false, cle
}

func (oc *AIClient) runStreamingWithRetry(
	ctx context.Context,
	evt *event.Event,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	promptContext PromptContext,
) {
	responseFn, logLabel := oc.selectStreamingRunFunc(meta, promptContext)
	success, err := oc.responseWithRetry(ctx, evt, portal, meta, promptContext, responseFn, logLabel)
	if success || err == nil {
		return
	}
	oc.notifyMatrixSendFailure(ctx, portal, evt, err)
}

func (oc *AIClient) selectStreamingRunFunc(meta *PortalMetadata, promptContext PromptContext) (responseFuncCanonical, string) {
	return oc.runResponsesStreamingPrompt, "responses"
}

func (oc *AIClient) notifyContextLengthExceeded(
	ctx context.Context,
	portal *bridgev2.Portal,
	cle *ContextLengthError,
	willRetry bool,
) {
	if cle == nil {
		return
	}
	message := "The conversation is too large for the selected model. Start a new chat or choose a larger-context model."
	oc.sendSystemNotice(ctx, portal, message)
}
