package connector

import (
	"context"

	"github.com/openai/openai-go/v3"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

// responseWithRetryAndReasoningFallback wraps responseWithRetry with reasoning level fallback logic.
// If the response fails with a reasoning-related error, it retries with a lower reasoning level.
func (oc *AIClient) responseWithRetryAndReasoningFallback(
	ctx context.Context,
	evt *event.Event,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	prompt []openai.ChatCompletionMessageParamUnion,
	responseFn responseFunc,
	logLabel string,
) (bool, error) {
	// Track attempted reasoning levels to avoid infinite loops
	attemptedLevels := make(map[string]bool)
	originalLevel := oc.effectiveReasoningEffort(meta)
	currentLevel := originalLevel
	maxReasoningFallbacks := 3
	var lastErr error

	for i := range maxReasoningFallbacks {
		attemptedLevels[currentLevel] = true

		// Create a modified meta with the current reasoning level if different from original
		effectiveMeta := meta
		if meta != nil && currentLevel != originalLevel {
			// Clone meta and override reasoning effort
			metaCopy := *meta
			metaCopy.ReasoningEffort = currentLevel
			effectiveMeta = &metaCopy
			oc.loggerForContext(ctx).Info().
				Str("original_level", originalLevel).
				Str("fallback_level", currentLevel).
				Msg("Retrying with lower reasoning level")
		}

		// Try the request with current reasoning level
		success, err := oc.responseWithRetry(ctx, evt, portal, effectiveMeta, prompt, responseFn, logLabel)
		if success {
			return true, nil
		}
		if err == nil {
			return false, nil
		}
		lastErr = err
		if !IsReasoningError(err) {
			return false, err
		}

		// Check if we should try a lower reasoning level
		fallbackLevel := FallbackReasoningLevel(currentLevel)
		if fallbackLevel == "" || attemptedLevels[fallbackLevel] {
			// No more fallbacks available or already tried
			return false, lastErr
		}

		currentLevel = fallbackLevel
	}

	if lastErr != nil {
		return false, lastErr
	}
	return false, nil
}

// Note: Reasoning error detection uses IsReasoningError on response errors.
