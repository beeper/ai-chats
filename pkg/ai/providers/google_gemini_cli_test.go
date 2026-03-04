package providers

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/beeper/ai-bridge/pkg/ai"
)

func TestExtractRetryDelay(t *testing.T) {
	now := time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC)

	t.Run("prefers Retry-After seconds header", func(t *testing.T) {
		headers := http.Header{"Retry-After": []string{"5"}}
		delay, ok := extractRetryDelayAt("Please retry in 1s", headers, now)
		if !ok || delay != 6000 {
			t.Fatalf("expected 6000ms, got %d (ok=%v)", delay, ok)
		}
	})

	t.Run("parses Retry-After HTTP date header", func(t *testing.T) {
		retryAt := now.Add(12 * time.Second).UTC().Format(http.TimeFormat)
		headers := http.Header{"Retry-After": []string{retryAt}}
		delay, ok := extractRetryDelayAt("", headers, now)
		if !ok || delay != 13000 {
			t.Fatalf("expected 13000ms, got %d (ok=%v)", delay, ok)
		}
	})

	t.Run("parses x-ratelimit-reset header", func(t *testing.T) {
		resetAt := now.Add(20 * time.Second).Unix()
		headers := http.Header{"x-ratelimit-reset": []string{strconv.FormatInt(resetAt, 10)}}
		delay, ok := extractRetryDelayAt("", headers, now)
		if !ok || delay != 21000 {
			t.Fatalf("expected 21000ms, got %d (ok=%v)", delay, ok)
		}
	})

	t.Run("parses x-ratelimit-reset-after header", func(t *testing.T) {
		headers := http.Header{"x-ratelimit-reset-after": []string{"30"}}
		delay, ok := extractRetryDelayAt("", headers, now)
		if !ok || delay != 31000 {
			t.Fatalf("expected 31000ms, got %d (ok=%v)", delay, ok)
		}
	})
}

func TestBuildGeminiCLIHeaders(t *testing.T) {
	t.Run("adds anthropic beta for claude thinking model", func(t *testing.T) {
		headers := BuildGeminiCLIHeaders(ai.Model{ID: "claude-opus-4-5-thinking"}, map[string]string{
			"authorization": "Bearer token",
		})
		if headers["anthropic-beta"] != ClaudeThinkingBetaHeader {
			t.Fatalf("expected anthropic-beta header %q, got %q", ClaudeThinkingBetaHeader, headers["anthropic-beta"])
		}
		if headers["authorization"] != "Bearer token" {
			t.Fatalf("expected existing headers to be preserved")
		}
	})

	t.Run("does not add anthropic beta for gemini model", func(t *testing.T) {
		headers := BuildGeminiCLIHeaders(ai.Model{ID: "gemini-2.5-flash"}, nil)
		if _, ok := headers["anthropic-beta"]; ok {
			t.Fatalf("did not expect anthropic-beta header for gemini model")
		}
	})
}

func TestGeminiEmptyStreamRetryHelpers(t *testing.T) {
	if delay, ok := GeminiEmptyStreamBackoff(1); !ok || delay != 500*time.Millisecond {
		t.Fatalf("expected first retry backoff 500ms, got %v (ok=%v)", delay, ok)
	}
	if delay, ok := GeminiEmptyStreamBackoff(2); !ok || delay != time.Second {
		t.Fatalf("expected second retry backoff 1s, got %v (ok=%v)", delay, ok)
	}
	if _, ok := GeminiEmptyStreamBackoff(0); ok {
		t.Fatalf("did not expect backoff for attempt 0")
	}
	if _, ok := GeminiEmptyStreamBackoff(3); ok {
		t.Fatalf("did not expect backoff beyond max retries")
	}

	if !ShouldRetryGeminiEmptyStream(false, 0) {
		t.Fatalf("expected retry on first empty attempt")
	}
	if !ShouldRetryGeminiEmptyStream(false, 1) {
		t.Fatalf("expected retry on second empty attempt")
	}
	if ShouldRetryGeminiEmptyStream(false, 2) {
		t.Fatalf("did not expect retry beyond max attempts")
	}
	if ShouldRetryGeminiEmptyStream(true, 0) {
		t.Fatalf("did not expect retry when content was received")
	}
}
