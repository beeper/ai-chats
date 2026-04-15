package ai

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"

	integrationruntime "github.com/beeper/agentremote/pkg/integrations/runtime"
	airuntime "github.com/beeper/agentremote/pkg/runtime"
)

const (
	maxRetryAttempts = 3 // Maximum retry attempts for context length errors
)

type responseFuncCanonical func(ctx context.Context, evt *event.Event, portal *bridgev2.Portal, meta *PortalMetadata, prompt PromptContext) (bool, *ContextLengthError, error)

// responseWithRetry wraps a response function with context length retry logic.
// It performs one runtime compaction retry attempt.
func (oc *AIClient) responseWithRetry(
	ctx context.Context,
	evt *event.Event,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	prompt PromptContext,
	responseFn responseFuncCanonical,
	logLabel string,
) (bool, error) {
	currentPrompt := ClonePromptContext(prompt)
	preflightFlushAttempted := false
	overflowCompactionAttempts := 0
	cachedTokenEstimate := -1
	var lastCLE *ContextLengthError

	for attempt := range maxRetryAttempts {
		if !preflightFlushAttempted {
			preflightFlushAttempted = true
			cachedTokenEstimate = oc.runCompactionPreflightFlushHook(ctx, portal, meta, currentPrompt, attempt+1)
		}

		success, cle, err := responseFn(ctx, evt, portal, meta, currentPrompt)
		if success {
			return true, nil
		}
		if err != nil {
			if errors.Is(err, context.Canceled) {
				if timeoutErr := agentLoopInactivityCause(ctx); timeoutErr != nil {
					oc.loggerForContext(ctx).Warn().Err(timeoutErr).Int("attempt", attempt+1).Str("log_label", logLabel).Msg("Agent loop timed out due to inactivity")
					return false, timeoutErr
				}
				return true, nil
			}
			oc.loggerForContext(ctx).Warn().Err(err).Int("attempt", attempt+1).Str("log_label", logLabel).Msg("Response attempt failed with error")
			return false, err
		}

		// If we got a context length error, run overflow compaction / truncation recovery.
		if cle != nil {
			lastCLE = cle
			oc.loggerForContext(ctx).Info().Int("attempt", attempt+1).Int("requested_tokens", cle.RequestedTokens).Int("max_tokens", cle.ModelMaxTokens).Str("log_label", logLabel).Msg("Context length exceeded, attempting recovery")
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
			tokensBefore := cachedTokenEstimate
			if tokensBefore < 0 {
				tokensBefore = estimatePromptContextTokensForModel(currentPrompt, modelID)
			}
			cachedTokenEstimate = -1 // invalidate after use

			if overflowCompactionAttempts < maxRetryAttempts {
				overflowCompactionAttempts++
				oc.runCompactionFlushHook(ctx, portal, meta, currentPrompt, cle, attempt+1)

				// Emit compaction start event.
				oc.emitCompactionLifecycle(ctx, integrationruntime.CompactionLifecycleEvent{
					Portal:              portal,
					Meta:                meta,
					Phase:               integrationruntime.CompactionLifecycleStart,
					Attempt:             attempt + 1,
					ContextWindowTokens: contextWindow,
					RequestedTokens:     cle.RequestedTokens,
					PromptTokens:        tokensBefore,
					MessagesBefore:      PromptContextMessageCount(currentPrompt),
					TokensBefore:        tokensBefore,
				})
				oc.emitCompactionStatus(ctx, portal, &CompactionEvent{
					Type:           CompactionEventStart,
					SessionID:      sessionID,
					MessagesBefore: PromptContextMessageCount(currentPrompt),
				})

				compacted, decision, compactionSuccess := oc.runtimeCompactOnOverflow(currentPrompt, contextWindow, cle.RequestedTokens, tokensBefore)
				if compactionSuccess && PromptContextMessageCount(compacted) > 2 {
					compacted = oc.applyCompactionModelSummaryAndRefresh(ctx, meta, currentPrompt, compacted, decision, contextWindow)
					tokensAfter := estimatePromptContextTokensForModel(compacted, modelID)
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
						MessagesBefore: PromptContextMessageCount(currentPrompt),
						MessagesAfter:  PromptContextMessageCount(compacted),
						TokensBefore:   tokensBefore,
						TokensAfter:    tokensAfter,
						Summary:        summary,
						WillRetry:      true,
					})
					oc.emitCompactionLifecyclePhases(ctx, integrationruntime.CompactionLifecycleEvent{
						Portal:              portal,
						Meta:                meta,
						Attempt:             attempt + 1,
						ContextWindowTokens: contextWindow,
						RequestedTokens:     cle.RequestedTokens,
						PromptTokens:        tokensAfter,
						MessagesBefore:      PromptContextMessageCount(currentPrompt),
						MessagesAfter:       PromptContextMessageCount(compacted),
						TokensBefore:        tokensBefore,
						TokensAfter:         tokensAfter,
						DroppedCount:        decision.DroppedCount,
						Reason:              decision.Reason,
						WillRetry:           true,
					}, integrationruntime.CompactionLifecycleEnd, integrationruntime.CompactionLifecycleRefresh)

					oc.loggerForContext(ctx).Info().
						Int("messages_before", PromptContextMessageCount(currentPrompt)).
						Int("messages_after", PromptContextMessageCount(compacted)).
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
					tokensAfter := estimatePromptContextTokensForModel(truncatedPrompt, modelID)
					oc.emitCompactionStatus(ctx, portal, &CompactionEvent{
						Type:           CompactionEventEnd,
						SessionID:      sessionID,
						MessagesBefore: PromptContextMessageCount(currentPrompt),
						MessagesAfter:  PromptContextMessageCount(truncatedPrompt),
						TokensBefore:   tokensBefore,
						TokensAfter:    tokensAfter,
						Summary:        fmt.Sprintf("Truncated %d oversized tool result(s).", truncatedCount),
						WillRetry:      true,
					})
					oc.emitCompactionLifecycle(ctx, integrationruntime.CompactionLifecycleEvent{
						Portal:              portal,
						Meta:                meta,
						Phase:               integrationruntime.CompactionLifecycleEnd,
						Attempt:             attempt + 1,
						ContextWindowTokens: contextWindow,
						RequestedTokens:     cle.RequestedTokens,
						PromptTokens:        tokensAfter,
						MessagesBefore:      PromptContextMessageCount(currentPrompt),
						MessagesAfter:       PromptContextMessageCount(truncatedPrompt),
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
					Portal:              portal,
					Meta:                meta,
					Phase:               integrationruntime.CompactionLifecycleFail,
					Attempt:             attempt + 1,
					ContextWindowTokens: contextWindow,
					RequestedTokens:     cle.RequestedTokens,
					PromptTokens:        tokensBefore,
					MessagesBefore:      PromptContextMessageCount(currentPrompt),
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

func (oc *AIClient) emitCompactionLifecyclePhases(ctx context.Context, base integrationruntime.CompactionLifecycleEvent, phases ...integrationruntime.CompactionLifecyclePhase) {
	for _, phase := range phases {
		event := base
		event.Phase = phase
		oc.emitCompactionLifecycle(ctx, event)
	}
}

func (oc *AIClient) runCompactionPreflightFlushHook(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	prompt PromptContext,
	attempt int,
) int {
	if oc == nil || meta == nil {
		return -1
	}
	contextWindow := oc.getModelContextWindow(meta)
	if contextWindow <= 0 {
		contextWindow = 128000
	}
	modelID := oc.effectiveModel(meta)
	promptTokens := estimatePromptContextTokensForModel(prompt, modelID)
	projectedTokens := projectedCompactionFlushTokens(meta, promptTokens)
	oc.emitCompactionLifecycle(ctx, integrationruntime.CompactionLifecycleEvent{
		Portal:              portal,
		Meta:                meta,
		Phase:               integrationruntime.CompactionLifecyclePreFlush,
		Attempt:             attempt,
		ContextWindowTokens: contextWindow,
		RequestedTokens:     projectedTokens,
		PromptTokens:        promptTokens,
		MessagesBefore:      PromptContextMessageCount(prompt),
		TokensBefore:        promptTokens,
	})
	oc.runCompactionFlushHook(ctx, portal, meta, prompt, &ContextLengthError{
		RequestedTokens: projectedTokens,
		ModelMaxTokens:  contextWindow,
	}, attempt)
	return promptTokens
}

func projectedCompactionFlushTokens(meta *PortalMetadata, promptTokens int) int {
	if promptTokens < 0 {
		promptTokens = 0
	}
	if meta == nil {
		return promptTokens
	}
	lastPrompt := int(meta.CompactionLastPromptTokens)
	lastOutput := int(meta.CompactionLastCompletionTokens)
	if lastPrompt <= 0 {
		return promptTokens
	}
	projected := lastPrompt + int(math.Max(0, float64(lastOutput))) + promptTokens
	if projected < promptTokens {
		return promptTokens
	}
	return projected
}

type overflowFlushHook interface {
	OnContextOverflow(ctx context.Context, call integrationruntime.ContextOverflowCall)
}

func (oc *AIClient) runCompactionFlushHook(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	prompt PromptContext,
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
		Portal:          portal,
		Meta:            meta,
		Prompt:          promptContextToChatCompletionMessages(prompt),
		RequestedTokens: cle.RequestedTokens,
		ModelMaxTokens:  cle.ModelMaxTokens,
		Attempt:         attempt,
	})
}

func (oc *AIClient) runAgentLoopWithRetry(
	ctx context.Context,
	evt *event.Event,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	promptContext PromptContext,
) {
	responseFn, logLabel := oc.selectAgentLoopRunFunc(meta, promptContext)
	success, err := oc.responseWithRetry(ctx, evt, portal, meta, promptContext, responseFn, logLabel)
	if success || err == nil {
		return
	}
	oc.notifyMatrixSendFailure(ctx, portal, evt, err)
}

func (oc *AIClient) selectAgentLoopRunFunc(meta *PortalMetadata, promptContext PromptContext) (responseFuncCanonical, string) {
	if hasUnsupportedResponsesPromptContext(promptContext) {
		return oc.runChatCompletionsAgentLoopPrompt, "chat_completions"
	}
	modelID := ""
	if oc != nil {
		modelID = oc.effectiveModel(meta)
	}
	switch oc.resolveModelAPI(meta) {
	case ModelAPIChatCompletions:
		if isDirectOpenAIModel(modelID) {
			return func(context.Context, *event.Event, *bridgev2.Portal, *PortalMetadata, PromptContext) (bool, *ContextLengthError, error) {
				return false, nil, fmt.Errorf("invalid model configuration: direct OpenAI model %q cannot use chat_completions", modelID)
			}, "invalid_model_api"
		}
		return oc.runChatCompletionsAgentLoopPrompt, "chat_completions"
	default:
		return oc.runResponsesAgentLoopPrompt, "responses"
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
	prompt PromptContext,
	contextWindowTokens int,
	requestedTokens int,
	currentPromptTokens int,
) (PromptContext, airuntime.CompactionDecision, bool) {
	serialized := promptContextToChatCompletionMessages(prompt)
	result := airuntime.CompactPromptOnOverflow(airuntime.OverflowCompactionInput{
		Prompt:              serialized,
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
	return chatMessagesToPromptContext(result.Prompt), result.Decision, result.Success
}

func (oc *AIClient) truncateOversizedToolResultsForOverflow(
	prompt PromptContext,
	contextWindowTokens int,
) (PromptContext, int) {
	if len(prompt.Messages) == 0 {
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

	out := ClonePromptContext(prompt)
	truncated := 0
	for i, msg := range out.Messages {
		if msg.Role != PromptRoleToolResult {
			continue
		}
		content := strings.TrimSpace(msg.Text())
		if len(content) <= thresholdChars {
			continue
		}
		trimmed := airuntime.SoftTrimToolResult(content, cfg)
		if trimmed == content {
			continue
		}
		out.Messages[i].Blocks = rewriteTrimmedToolResultBlocks(msg.Blocks, trimmed)
		truncated++
	}
	return out, truncated
}

func rewriteTrimmedToolResultBlocks(blocks []PromptBlock, trimmed string) []PromptBlock {
	if len(blocks) == 0 {
		return []PromptBlock{{Type: PromptBlockText, Text: trimmed}}
	}
	remaining := trimmed
	rewritten := make([]PromptBlock, 0, len(blocks))
	previousTextBlock := false
	for _, block := range blocks {
		if remaining == "" {
			break
		}
		switch block.Type {
		case PromptBlockText, PromptBlockThinking:
		default:
			continue
		}
		if block.Text == "" {
			continue
		}
		if previousTextBlock && strings.HasPrefix(remaining, "\n") {
			remaining = remaining[1:]
			if remaining == "" {
				break
			}
		}
		take := len(block.Text)
		if take > len(remaining) {
			take = len(remaining)
		}
		block.Text = remaining[:take]
		remaining = remaining[take:]
		rewritten = append(rewritten, block)
		previousTextBlock = true
	}
	if len(rewritten) == 0 {
		return []PromptBlock{{Type: PromptBlockText, Text: trimmed}}
	}
	return rewritten
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
	if _, _, err := oc.sendViaPortalWithTiming(ctx, portal, converted, "", time.Now(), 0); err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).
			Str("type", string(evt.Type)).
			Msg("Failed to emit compaction status event")
	}
}
