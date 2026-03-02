package connector

import (
	"context"
	"errors"
	"fmt"
	"math"

	integrationruntime "github.com/beeper/ai-bridge/pkg/integrations/runtime"
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
// It performs one runtime compaction retry attempt.
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
	preflightFlushAttempted := false
	overflowCompactionAttempts := 0
	var lastCLE *ContextLengthError

	for attempt := range maxRetryAttempts {
		if !preflightFlushAttempted {
			preflightFlushAttempted = true
			oc.runCompactionPreflightFlushHook(ctx, portal, meta, currentPrompt, attempt+1)
		}

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

		// If we got a context length error, run overflow compaction / truncation recovery.
		if cle != nil {
			lastCLE = cle
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

			// Get context window from model.
			contextWindow := oc.getModelContextWindow(meta)
			if contextWindow <= 0 {
				contextWindow = 128000 // Default fallback
			}
			sessionID := string(portal.MXID)
			modelID := ""
			if meta != nil {
				modelID = oc.effectiveModel(meta)
			}
			tokensBefore := estimatePromptTokensForModel(currentPrompt, modelID)

			if overflowCompactionAttempts < maxRetryAttempts {
				overflowCompactionAttempts++
				oc.runCompactionFlushHook(ctx, portal, meta, currentPrompt, cle, attempt+1)

				// Emit compaction start event.
				oc.emitCompactionLifecycle(ctx, integrationruntime.CompactionLifecycleEvent{
					Client:              oc,
					Portal:              portal,
					Meta:                meta,
					Phase:               integrationruntime.CompactionLifecycleStart,
					Attempt:             attempt + 1,
					ContextWindowTokens: contextWindow,
					RequestedTokens:     cle.RequestedTokens,
					PromptTokens:        tokensBefore,
					MessagesBefore:      len(currentPrompt),
					TokensBefore:        tokensBefore,
				})
				oc.emitCompactionStatus(ctx, portal, &CompactionEvent{
					Type:           CompactionEventStart,
					SessionID:      sessionID,
					MessagesBefore: len(currentPrompt),
				})

				compacted, decision, compactionSuccess := oc.runtimeCompactOnOverflow(currentPrompt, contextWindow, cle.RequestedTokens, tokensBefore)
				if compactionSuccess && len(compacted) > 2 {
					compacted = oc.applyCompactionModelSummaryAndRefresh(ctx, meta, currentPrompt, compacted, decision, contextWindow)
					tokensAfter := estimatePromptTokensForModel(compacted, modelID)
					if meta != nil {
						meta.CompactionCount++
						oc.savePortalQuiet(ctx, portal, "compaction count")
					}
					summary := ""
					if decision.DroppedCount > 0 {
						summary = fmt.Sprintf("Dropped %d older context entries.", decision.DroppedCount)
					}
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
					oc.emitCompactionLifecycle(ctx, integrationruntime.CompactionLifecycleEvent{
						Client:              oc,
						Portal:              portal,
						Meta:                meta,
						Phase:               integrationruntime.CompactionLifecycleEnd,
						Attempt:             attempt + 1,
						ContextWindowTokens: contextWindow,
						RequestedTokens:     cle.RequestedTokens,
						PromptTokens:        tokensAfter,
						MessagesBefore:      len(currentPrompt),
						MessagesAfter:       len(compacted),
						TokensBefore:        tokensBefore,
						TokensAfter:         tokensAfter,
						DroppedCount:        decision.DroppedCount,
						Reason:              decision.Reason,
						WillRetry:           true,
					})
					oc.emitCompactionLifecycle(ctx, integrationruntime.CompactionLifecycleEvent{
						Client:              oc,
						Portal:              portal,
						Meta:                meta,
						Phase:               integrationruntime.CompactionLifecycleRefresh,
						Attempt:             attempt + 1,
						ContextWindowTokens: contextWindow,
						RequestedTokens:     cle.RequestedTokens,
						PromptTokens:        tokensAfter,
						MessagesBefore:      len(currentPrompt),
						MessagesAfter:       len(compacted),
						TokensBefore:        tokensBefore,
						TokensAfter:         tokensAfter,
						DroppedCount:        decision.DroppedCount,
						Reason:              decision.Reason,
						WillRetry:           true,
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

				// Compaction was insufficient. Try an explicit tool-result truncation pass.
				truncatedPrompt, truncatedCount := oc.truncateOversizedToolResultsForOverflow(currentPrompt, contextWindow)
				if truncatedCount > 0 {
					tokensAfter := estimatePromptTokensForModel(truncatedPrompt, modelID)
					oc.emitCompactionStatus(ctx, portal, &CompactionEvent{
						Type:           CompactionEventEnd,
						SessionID:      sessionID,
						MessagesBefore: len(currentPrompt),
						MessagesAfter:  len(truncatedPrompt),
						TokensBefore:   tokensBefore,
						TokensAfter:    tokensAfter,
						Summary:        fmt.Sprintf("Truncated %d oversized tool result(s).", truncatedCount),
						WillRetry:      true,
					})
					oc.emitCompactionLifecycle(ctx, integrationruntime.CompactionLifecycleEvent{
						Client:              oc,
						Portal:              portal,
						Meta:                meta,
						Phase:               integrationruntime.CompactionLifecycleEnd,
						Attempt:             attempt + 1,
						ContextWindowTokens: contextWindow,
						RequestedTokens:     cle.RequestedTokens,
						PromptTokens:        tokensAfter,
						MessagesBefore:      len(currentPrompt),
						MessagesAfter:       len(truncatedPrompt),
						TokensBefore:        tokensBefore,
						TokensAfter:         tokensAfter,
						Reason:              "truncate_oversized_tool_results",
						WillRetry:           true,
					})
					oc.loggerForContext(ctx).Info().
						Int("truncated_count", truncatedCount).
						Int("tokens_before", tokensBefore).
						Int("tokens_after", tokensAfter).
						Msg("Compaction fallback truncated oversized tool results, retrying")
					currentPrompt = truncatedPrompt
					continue
				}

				oc.emitCompactionStatus(ctx, portal, &CompactionEvent{
					Type:      CompactionEventEnd,
					SessionID: sessionID,
					Error:     "compaction did not reduce context sufficiently",
				})
				oc.emitCompactionLifecycle(ctx, integrationruntime.CompactionLifecycleEvent{
					Client:              oc,
					Portal:              portal,
					Meta:                meta,
					Phase:               integrationruntime.CompactionLifecycleFail,
					Attempt:             attempt + 1,
					ContextWindowTokens: contextWindow,
					RequestedTokens:     cle.RequestedTokens,
					PromptTokens:        tokensBefore,
					MessagesBefore:      len(currentPrompt),
					TokensBefore:        tokensBefore,
					Reason:              "compaction did not reduce context sufficiently",
					Error:               "compaction did not reduce context sufficiently",
				})
			}

			oc.notifyContextLengthExceeded(ctx, portal, cle, false)
			return false, cle
		}

		// Non-context nil error from responseFn: treat as a terminal failure.
		return false, errors.New("response failed without context length detail")
	}

	if lastCLE != nil {
		oc.notifyContextLengthExceeded(ctx, portal, lastCLE, false)
		return false, fmt.Errorf("exceeded maximum retry attempts for prompt overflow: %w", lastCLE)
	}
	terminal := errors.New("exceeded maximum retry attempts for prompt overflow")
	oc.notifyMatrixSendFailure(ctx, portal, evt, terminal)
	return false, terminal
}

func (oc *AIClient) runCompactionPreflightFlushHook(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	prompt []openai.ChatCompletionMessageParamUnion,
	attempt int,
) {
	if oc == nil || meta == nil {
		return
	}
	contextWindow := oc.getModelContextWindow(meta)
	if contextWindow <= 0 {
		contextWindow = 128000
	}
	modelID := oc.effectiveModel(meta)
	promptTokens := estimatePromptTokensForModel(prompt, modelID)
	projectedTokens := projectedCompactionFlushTokens(meta, promptTokens)
	oc.emitCompactionLifecycle(ctx, integrationruntime.CompactionLifecycleEvent{
		Client:              oc,
		Portal:              portal,
		Meta:                meta,
		Phase:               integrationruntime.CompactionLifecyclePreFlush,
		Attempt:             attempt,
		ContextWindowTokens: contextWindow,
		RequestedTokens:     projectedTokens,
		PromptTokens:        promptTokens,
		MessagesBefore:      len(prompt),
		TokensBefore:        promptTokens,
	})
	oc.runCompactionFlushHook(ctx, portal, meta, prompt, &ContextLengthError{
		RequestedTokens: projectedTokens,
		ModelMaxTokens:  contextWindow,
	}, attempt)
}

func projectedCompactionFlushTokens(meta *PortalMetadata, promptTokens int) int {
	if promptTokens < 0 {
		promptTokens = 0
	}
	if meta == nil {
		return promptTokens
	}
	lastPrompt := int(moduleMetaNumber(meta, "compaction_last_prompt_tokens"))
	lastOutput := int(moduleMetaNumber(meta, "compaction_last_completion_tokens"))
	if lastPrompt <= 0 {
		return promptTokens
	}
	projected := lastPrompt + int(math.Max(0, float64(lastOutput))) + promptTokens
	if projected < promptTokens {
		return promptTokens
	}
	return projected
}

func moduleMetaNumber(meta *PortalMetadata, key string) int64 {
	if meta == nil || meta.ModuleMeta == nil || key == "" {
		return 0
	}
	raw, ok := meta.ModuleMeta[key]
	if !ok || raw == nil {
		return 0
	}
	switch v := raw.(type) {
	case int:
		return int64(v)
	case int64:
		return v
	case float64:
		return int64(v)
	default:
		return 0
	}
}

type overflowFlushHook interface {
	OnContextOverflow(ctx context.Context, call integrationruntime.ContextOverflowCall)
}

func (oc *AIClient) runCompactionFlushHook(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	prompt []openai.ChatCompletionMessageParamUnion,
	cle *ContextLengthError,
	attempt int,
) {
	if oc == nil || meta == nil || cle == nil {
		return
	}
	cfg := oc.pruningOverflowFlushConfig()
	if cfg == nil {
		return
	}
	if cfg.Enabled != nil && !*cfg.Enabled {
		return
	}
	if oc.integrationModules == nil {
		return
	}
	module, ok := oc.integrationModules["memory"]
	if !ok || module == nil {
		return
	}
	hook, ok := module.(overflowFlushHook)
	if !ok {
		return
	}
	hook.OnContextOverflow(ctx, integrationruntime.ContextOverflowCall{
		Client:          oc,
		Portal:          portal,
		Meta:            meta,
		Prompt:          prompt,
		RequestedTokens: cle.RequestedTokens,
		ModelMaxTokens:  cle.ModelMaxTokens,
		Attempt:         attempt,
	})
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
	currentPromptTokens int,
) ([]openai.ChatCompletionMessageParamUnion, airuntime.CompactionDecision, bool) {
	result := airuntime.CompactPromptOnOverflow(airuntime.OverflowCompactionInput{
		Prompt:              prompt,
		ContextWindowTokens: contextWindowTokens,
		RequestedTokens:     requestedTokens,
		CurrentPromptTokens: currentPromptTokens,
		ReserveTokens:       oc.pruningReserveTokens(),
		KeepRecentTokens:    oc.pruningKeepRecentTokens(),
		CompactionMode:      oc.pruningCompactionMode(),
		Summarization:       false,
		MaxSummaryTokens:    oc.pruningMaxSummaryTokens(),
		RefreshPrompt:       "",
		MaxHistoryShare:     oc.pruningMaxHistoryShare(),
		ProtectedTail:       3,
	})
	return result.Prompt, result.Decision, result.Success
}

func (oc *AIClient) truncateOversizedToolResultsForOverflow(
	prompt []openai.ChatCompletionMessageParamUnion,
	contextWindowTokens int,
) ([]openai.ChatCompletionMessageParamUnion, int) {
	if len(prompt) == 0 {
		return prompt, 0
	}
	cfg := oc.pruningConfigOrDefault()
	if cfg == nil {
		cfg = airuntime.DefaultPruningConfig()
	}
	maxChars := cfg.SoftTrimMaxChars
	if maxChars <= 0 {
		maxChars = 4000
	}
	thresholdChars := maxChars * 2
	if contextWindowTokens > 0 {
		windowThreshold := (contextWindowTokens * airuntime.CharsPerTokenEstimate) / 4
		if windowThreshold > thresholdChars {
			thresholdChars = windowThreshold
		}
	}

	out := append([]openai.ChatCompletionMessageParamUnion{}, prompt...)
	truncated := 0
	for i, msg := range out {
		if msg.OfTool == nil {
			continue
		}
		content := airuntime.ExtractToolContent(msg.OfTool.Content)
		if len(content) <= thresholdChars {
			continue
		}
		trimmed := airuntime.SoftTrimToolResult(content, cfg)
		if trimmed == content {
			continue
		}
		out[i] = openai.ToolMessage(trimmed, msg.OfTool.ToolCallID)
		truncated++
	}
	return out, truncated
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
