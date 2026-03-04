package providers

import (
	"net/http"
	"strconv"
	"testing"
	"time"
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
