package connector

import (
	"context"
	"errors"
	"fmt"

	airuntime "github.com/beeper/ai-bridge/pkg/runtime"
	"github.com/openai/openai-go/v3"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
)

const (
	maxRetryAttempts = 3 // Maximum retry attempts for context length errors
)

// responseFunc is the signature for response handlers that can be retried on context length errors
type responseFunc func(ctx context.Context, evt *event.Event, portal *bridgev2.Portal, meta *PortalMetadata, prompt []openai.ChatCompletionMessageParamUnion) (bool, *ContextLengthError, error)

// responseWithRetry wraps a response function with context length retry logic.
// It performs one runtime compaction attempt before falling back to reactive truncation.
func (oc *AIClient) responseWithRetry(
	ctx context.Context,
	evt *event.Event,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	prompt []openai.ChatCompletionMessageParamUnion,
	responseFn responseFunc,
	logLabel string,
) (bool, error) {
	currentPrompt := prompt
	autoCompactionAttempted := false

	for attempt := range maxRetryAttempts {
		success, cle, err := responseFn(ctx, evt, portal, meta, currentPrompt)
		if success {
			return true, nil
		}
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return true, nil
			}
			oc.loggerForContext(ctx).Warn().Err(err).Int("attempt", attempt+1).Str("log_label", logLabel).Msg("Response attempt failed with error")
			return false, err
		}

		// If we got a context length error, try auto-compaction first, then truncation
		if cle != nil {
			oc.loggerForContext(ctx).Info().Int("attempt", attempt+1).Int("requested_tokens", cle.RequestedTokens).Int("max_tokens", cle.ModelMaxTokens).Str("log_label", logLabel).Msg("Context length exceeded, attempting recovery")
			// In Responses conversation mode, previous_response_id can accumulate hidden server-side
			// context that local truncation cannot affect. Reset it once and retry with local history.
			if meta != nil && meta.ConversationMode == "responses" && meta.LastResponseID != "" && !oc.isOpenRouterProvider() {
				oc.loggerForContext(ctx).Warn().
					Str("last_response_id", meta.LastResponseID).
					Msg("Context overflow in responses mode; clearing previous_response_id and retrying with local context")
				meta.LastResponseID = ""
				oc.savePortalQuiet(ctx, portal, "responses context reset")
				continue
			}

			// Try auto-compaction first (only once per retry loop)
			if !autoCompactionAttempted {
				autoCompactionAttempted = true

				// Get context window from model
				contextWindow := oc.getModelContextWindow(meta)
				if contextWindow <= 0 {
					contextWindow = 128000 // Default fallback
				}

				// Emit compaction start event
				sessionID := string(portal.MXID)
				oc.emitCompactionStatus(ctx, portal, &CompactionEvent{
					Type:           CompactionEventStart,
					SessionID:      sessionID,
					MessagesBefore: len(currentPrompt),
				})

				compacted, decision, compactionSuccess := oc.runtimeCompactOnOverflow(currentPrompt, contextWindow, cle.RequestedTokens)

				if compactionSuccess && len(compacted) > 2 {
					modelID := ""
					if meta != nil {
						modelID = oc.effectiveModel(meta)
					}
					tokensBefore := estimatePromptTokensForModel(currentPrompt, modelID)
					tokensAfter := estimatePromptTokensForModel(compacted, modelID)
					if meta != nil {
						meta.CompactionCount++
						oc.savePortalQuiet(ctx, portal, "compaction count")
					}
					summary := ""
					if decision.DroppedCount > 0 {
						summary = fmt.Sprintf("Dropped %d older context entries.", decision.DroppedCount)
					}
					// Emit compaction end event
					oc.emitCompactionStatus(ctx, portal, &CompactionEvent{
						Type:           CompactionEventEnd,
						SessionID:      sessionID,
						MessagesBefore: len(currentPrompt),
						MessagesAfter:  len(compacted),
						TokensBefore:   tokensBefore,
						TokensAfter:    tokensAfter,
						Summary:        summary,
						WillRetry:      true,
					})

					oc.loggerForContext(ctx).Info().
						Int("messages_before", len(currentPrompt)).
						Int("messages_after", len(compacted)).
						Int("tokens_before", tokensBefore).
						Int("tokens_after", tokensAfter).
						Int("dropped", decision.DroppedCount).
						Msg("Auto-compaction succeeded, retrying with compacted context")

					currentPrompt = compacted
					continue
				}

				// Compaction failed or didn't help enough
				oc.emitCompactionStatus(ctx, portal, &CompactionEvent{
					Type:      CompactionEventEnd,
					SessionID: sessionID,
					Error:     "compaction did not reduce context sufficiently",
				})

				oc.loggerForContext(ctx).Warn().Msg("Auto-compaction did not help, falling back to reactive truncation")
			}

			oc.notifyContextLengthExceeded(ctx, portal, cle, false)
			return false, cle
		}

		// Non-context error, already handled in responseFn
		return false, nil
	}

	oc.notifyMatrixSendFailure(ctx, portal, evt,
		errors.New("exceeded maximum retry attempts for prompt overflow"))
	return false, nil
}

func (oc *AIClient) streamingResponseWithRetry(
	ctx context.Context,
	evt *event.Event,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	prompt []openai.ChatCompletionMessageParamUnion,
) {
	selector := func(meta *PortalMetadata, prompt []openai.ChatCompletionMessageParamUnion) (responseFunc, string) {
		return oc.selectResponseFn(meta, prompt)
	}
	oc.responseWithModelFallbackDynamic(ctx, evt, portal, meta, prompt, selector)
}

func (oc *AIClient) selectResponseFn(meta *PortalMetadata, prompt []openai.ChatCompletionMessageParamUnion) (responseFunc, string) {
	// Use Chat Completions API for audio (native support)
	// SDK v3.16.0 has ResponseInputAudioParam but it's not wired into the union
	if hasAudioContent(prompt) {
		return oc.streamChatCompletions, "chat_completions"
	}
	switch oc.resolveModelAPI(meta) {
	case ModelAPIChatCompletions:
		return oc.streamChatCompletions, "chat_completions"
	default:
		// Use Responses API for other content (images, files, text)
		return oc.streamingResponseWithToolSchemaFallback, "responses"
	}
}

// notifyContextLengthExceeded sends a user-friendly notice about context overflow
func (oc *AIClient) notifyContextLengthExceeded(
	ctx context.Context,
	portal *bridgev2.Portal,
	cle *ContextLengthError,
	willRetry bool,
) {
	var message string
	if willRetry {
		message = fmt.Sprintf(
			"Your conversation exceeded the model's context limit (%d tokens requested, %d max). "+
				"Automatically trimming older messages and retrying...",
			cle.RequestedTokens, cle.ModelMaxTokens,
		)
	} else {
		message = fmt.Sprintf(
			"Your message is too long for this model's context window (%d tokens max). "+
				"Try a shorter message, or start a new conversation.",
			cle.ModelMaxTokens,
		)
	}

	oc.sendSystemNotice(ctx, portal, message)
}

func (oc *AIClient) runtimeCompactOnOverflow(
	prompt []openai.ChatCompletionMessageParamUnion,
	contextWindowTokens int,
	requestedTokens int,
) ([]openai.ChatCompletionMessageParamUnion, airuntime.CompactionDecision, bool) {
	result := airuntime.CompactPromptOnOverflow(airuntime.OverflowCompactionInput{
		Prompt:              prompt,
		ContextWindowTokens: contextWindowTokens,
		RequestedTokens:     requestedTokens,
		ReserveTokens:       oc.pruningReserveTokens(),
		ProtectedTail:       3,
	})
	return result.Prompt, result.Decision, result.Success
}

// emitCompactionStatus sends a compaction status event to the room
func (oc *AIClient) emitCompactionStatus(ctx context.Context, portal *bridgev2.Portal, evt *CompactionEvent) {
	if portal == nil || portal.MXID == "" {
		return
	}

	content := map[string]any{
		"type":       string(evt.Type),
		"session_id": evt.SessionID,
	}

	if evt.MessagesBefore > 0 {
		content["messages_before"] = evt.MessagesBefore
	}
	if evt.MessagesAfter > 0 {
		content["messages_after"] = evt.MessagesAfter
	}
	if evt.TokensBefore > 0 {
		content["tokens_before"] = evt.TokensBefore
	}
	if evt.TokensAfter > 0 {
		content["tokens_after"] = evt.TokensAfter
	}
	if evt.Summary != "" {
		content["summary"] = evt.Summary
	}
	if evt.WillRetry {
		content["will_retry"] = evt.WillRetry
	}
	if evt.Error != "" {
		content["error"] = evt.Error
	}

	converted := &bridgev2.ConvertedMessage{
		Parts: []*bridgev2.ConvertedMessagePart{{
			ID:    networkid.PartID("0"),
			Type:  CompactionStatusEventType,
			Extra: content,
		}},
	}
	if _, _, err := oc.sendViaPortal(ctx, portal, converted, ""); err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).
			Str("type", string(evt.Type)).
			Msg("Failed to emit compaction status event")
	}
}
