package connector

import (
	"context"

	"github.com/openai/openai-go/v3"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

func (oc *AIClient) streamingResponseWithToolSchemaFallback(
	ctx context.Context,
	evt *event.Event,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	messages []openai.ChatCompletionMessageParamUnion,
) (bool, *ContextLengthError, error) {
	success, cle, err := oc.streamingResponse(ctx, evt, portal, meta, messages)
	if success || cle != nil || err == nil {
		return success, cle, err
	}
	if IsToolUniquenessError(err) {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Duplicate tool names rejected; retrying with chat completions")
		success, cle, chatErr := oc.streamChatCompletions(ctx, evt, portal, meta, messages)
		if success || cle != nil || chatErr == nil {
			return success, cle, chatErr
		}
		if IsToolSchemaError(chatErr) || IsToolUniquenessError(chatErr) {
			oc.loggerForContext(ctx).Warn().Err(chatErr).Msg("Chat completions tools rejected; retrying without tools")
			if meta != nil {
				metaCopy := *meta
				metaCopy.Capabilities = meta.Capabilities
				metaCopy.Capabilities.SupportsToolCalling = false
				return oc.streamChatCompletions(ctx, evt, portal, &metaCopy, messages)
			}
		}
		return success, cle, chatErr
	}
	if IsToolSchemaError(err) {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Responses tool schema rejected; falling back to chat completions")
		success, cle, chatErr := oc.streamChatCompletions(ctx, evt, portal, meta, messages)
		if success || cle != nil || chatErr == nil {
			return success, cle, chatErr
		}
		if IsToolSchemaError(chatErr) {
			oc.loggerForContext(ctx).Warn().Err(chatErr).Msg("Chat completions tool schema rejected; retrying without tools")
			if meta != nil {
				metaCopy := *meta
				metaCopy.Capabilities = meta.Capabilities
				metaCopy.Capabilities.SupportsToolCalling = false
				return oc.streamChatCompletions(ctx, evt, portal, &metaCopy, messages)
			}
		}
		return success, cle, chatErr
	}
	if IsNoResponseChunksError(err) {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Responses streaming returned no chunks; retrying without tools")
		if meta != nil && meta.Capabilities.SupportsToolCalling {
			metaCopy := *meta
			metaCopy.Capabilities = meta.Capabilities
			metaCopy.Capabilities.SupportsToolCalling = false
			success, cle, retryErr := oc.streamingResponse(ctx, evt, portal, &metaCopy, messages)
			if success || cle != nil || retryErr == nil {
				return success, cle, retryErr
			}
			err = retryErr
		}
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Responses retry failed; falling back to chat completions")
		return oc.streamChatCompletions(ctx, evt, portal, meta, messages)
	}
	return success, cle, err
}
