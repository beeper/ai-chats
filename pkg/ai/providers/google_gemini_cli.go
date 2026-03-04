package providers

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/beeper/ai-bridge/pkg/ai"
)

const ClaudeThinkingBetaHeader = "interleaved-thinking-2025-05-14"
const (
	MaxGeminiEmptyStreamRetries = 2
	EmptyStreamBaseDelayMs      = 500
)

func ExtractRetryDelay(errorText string, headers http.Header) (int, bool) {
	return extractRetryDelayAt(errorText, headers, time.Now())
}

func extractRetryDelayAt(errorText string, headers http.Header, now time.Time) (int, bool) {
	normalizeDelay := func(ms float64) (int, bool) {
		if ms <= 0 {
			return 0, false
		}
		return int(ms + 1000), true
	}

	if headers != nil {
		retryAfter := headerGetCI(headers, "Retry-After")
		if retryAfter != "" {
			if secs, err := strconv.ParseFloat(strings.TrimSpace(retryAfter), 64); err == nil {
				if delay, ok := normalizeDelay(secs * 1000); ok {
					return delay, true
				}
			}
			if retryAt, err := http.ParseTime(retryAfter); err == nil {
				if delay, ok := normalizeDelay(float64(retryAt.Sub(now).Milliseconds())); ok {
					return delay, true
				}
			}
		}

		if reset := headerGetCI(headers, "x-ratelimit-reset"); reset != "" {
			if sec, err := strconv.ParseInt(strings.TrimSpace(reset), 10, 64); err == nil {
				resetAt := time.Unix(sec, 0)
				if delay, ok := normalizeDelay(float64(resetAt.Sub(now).Milliseconds())); ok {
					return delay, true
				}
			}
		}

		if resetAfter := headerGetCI(headers, "x-ratelimit-reset-after"); resetAfter != "" {
			if secs, err := strconv.ParseFloat(strings.TrimSpace(resetAfter), 64); err == nil {
				if delay, ok := normalizeDelay(secs * 1000); ok {
					return delay, true
				}
			}
		}
	}

	durationPattern := regexp.MustCompile(`(?i)reset after (?:(\d+)h)?(?:(\d+)m)?(\d+(?:\.\d+)?)s`)
	if matches := durationPattern.FindStringSubmatch(errorText); len(matches) == 4 {
		hours, _ := strconv.ParseFloat(orZero(matches[1]), 64)
		minutes, _ := strconv.ParseFloat(orZero(matches[2]), 64)
		seconds, _ := strconv.ParseFloat(orZero(matches[3]), 64)
		ms := ((hours*60+minutes)*60 + seconds) * 1000
		if delay, ok := normalizeDelay(ms); ok {
			return delay, true
		}
	}

	retryInPattern := regexp.MustCompile(`(?i)Please retry in ([0-9.]+)(ms|s)`)
	if matches := retryInPattern.FindStringSubmatch(errorText); len(matches) == 3 {
		value, _ := strconv.ParseFloat(matches[1], 64)
		if strings.EqualFold(matches[2], "s") {
			value *= 1000
		}
		if delay, ok := normalizeDelay(value); ok {
			return delay, true
		}
	}

	retryDelayPattern := regexp.MustCompile(`(?i)"retryDelay":\s*"([0-9.]+)(ms|s)"`)
	if matches := retryDelayPattern.FindStringSubmatch(errorText); len(matches) == 3 {
		value, _ := strconv.ParseFloat(matches[1], 64)
		if strings.EqualFold(matches[2], "s") {
			value *= 1000
		}
		if delay, ok := normalizeDelay(value); ok {
			return delay, true
		}
	}

	return 0, false
}

func orZero(in string) string {
	if strings.TrimSpace(in) == "" {
		return "0"
	}
	return in
}

func headerGetCI(headers http.Header, key string) string {
	if headers == nil {
		return ""
	}
	if v := headers.Get(key); v != "" {
		return v
	}
	for k, values := range headers {
		if !strings.EqualFold(k, key) || len(values) == 0 {
			continue
		}
		return values[0]
	}
	return ""
}

func NormalizeGoogleToolCall(name string, args map[string]any, id string, thoughtSignature string) ai.ContentBlock {
	normalizedArgs := args
	if normalizedArgs == nil {
		normalizedArgs = map[string]any{}
	}
	block := ai.ContentBlock{
		Type:      ai.ContentTypeToolCall,
		ID:        id,
		Name:      name,
		Arguments: normalizedArgs,
	}
	if strings.TrimSpace(thoughtSignature) != "" {
		block.ThoughtSignature = thoughtSignature
	}
	return block
}

func IsClaudeThinkingModel(modelID string) bool {
	normalized := strings.ToLower(strings.TrimSpace(modelID))
	return strings.Contains(normalized, "claude") && strings.Contains(normalized, "thinking")
}

func BuildGeminiCLIHeaders(model ai.Model, headers map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range headers {
		out[k] = v
	}
	if IsClaudeThinkingModel(model.ID) {
		out["anthropic-beta"] = ClaudeThinkingBetaHeader
	}
	return out
}

func GeminiEmptyStreamBackoff(attempt int) (time.Duration, bool) {
	if attempt <= 0 || attempt > MaxGeminiEmptyStreamRetries {
		return 0, false
	}
	delayMs := EmptyStreamBaseDelayMs * (1 << (attempt - 1))
	return time.Duration(delayMs) * time.Millisecond, true
}

func ShouldRetryGeminiEmptyStream(hasContent bool, emptyAttempt int) bool {
	return !hasContent && emptyAttempt < MaxGeminiEmptyStreamRetries
}
