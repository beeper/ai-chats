package connector

import (
	"strings"
	"time"
)

// anthropicCacheTTL is the prompt cache window for Anthropic models.
const anthropicCacheTTL = 5 * time.Minute

// IsCacheTTLEligibleProvider returns true if the model is served by Anthropic
// (directly or via OpenRouter) and thus eligible for prompt caching.
func IsCacheTTLEligibleProvider(model string) bool {
	lower := strings.ToLower(model)
	return strings.HasPrefix(lower, "anthropic/") ||
		strings.Contains(lower, "claude") ||
		// OpenRouter routes anthropic/ prefixed models to Anthropic
		(strings.Contains(lower, "openrouter") && strings.Contains(lower, "anthropic"))
}

// AppendCacheTTLTimestamp records the current time as the last cache-eligible
// request timestamp on the portal metadata.
func AppendCacheTTLTimestamp(meta *PortalMetadata) {
	if meta == nil {
		return
	}
	meta.LastCacheTTLRefresh = time.Now().UnixMilli()
}

// ShouldRefreshCacheTTL returns true if the Anthropic prompt cache TTL window
// is about to expire (or has expired) and a cache-warming request should include
// a cache_control breakpoint.
func ShouldRefreshCacheTTL(meta *PortalMetadata) bool {
	if meta == nil || meta.LastCacheTTLRefresh == 0 {
		return true
	}
	elapsed := time.Since(time.UnixMilli(meta.LastCacheTTLRefresh))
	return elapsed >= anthropicCacheTTL
}
