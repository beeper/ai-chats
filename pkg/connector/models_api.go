package connector

import "strings"

// OpenClawAliases provides OpenClaw-compatible shorthands and model ID aliases.
// These resolve to canonical model IDs in the local manifest.
var OpenClawAliases = map[string]string{
	// OpenClaw built-in shorthands
	"opus":         "anthropic/claude-opus-4.6",
	"sonnet":       "anthropic/claude-sonnet-4.5",
	"haiku":        "anthropic/claude-haiku-4.5",
	"gpt":          "openai/gpt-5.2",
	"gpt-mini":     "openai/gpt-5-mini",
	"gemini":       "google/gemini-3-pro-preview",
	"gemini-flash": "google/gemini-3-flash-preview",

	// OpenRouter expects the dotted major.minor model IDs. Map base Anthropic
	// ids (often used in other APIs) to our canonical OpenRouter IDs.
	"anthropic/claude-opus-4":   "anthropic/claude-opus-4.6",
	"anthropic/claude-sonnet-4": "anthropic/claude-sonnet-4.5",
	"anthropic/claude-haiku-4":  "anthropic/claude-haiku-4.5",

	// OpenClaw model ID variants
	"anthropic/claude-opus-4-5":   "anthropic/claude-opus-4.5",
	"anthropic/claude-sonnet-4-5": "anthropic/claude-sonnet-4.5",
	"anthropic/claude-haiku-4-5":  "anthropic/claude-haiku-4.5",
	"zai/glm-4.7":                 "z-ai/glm-4.7",

	// OpenClaw provider IDs that differ from OpenRouter IDs
	"minimax/MiniMax-M2.1":          "minimax/minimax-m2.1",
	"minimax/MiniMax-M2":            "minimax/minimax-m2",
	"moonshot/kimi-k2.5":            "moonshotai/kimi-k2.5",
	"moonshot/kimi-k2-0905":         "moonshotai/kimi-k2-0905",
	"moonshot/kimi-k2-0905-preview": "moonshotai/kimi-k2-0905",
	"moonshot/kimi-k2-thinking":     "moonshotai/kimi-k2-thinking",
}

// Model API provides a unified interface for looking up models and aliases.

// GetModelDisplayName returns a human-readable display name for a model.
func GetModelDisplayName(modelID string) string {
	// Resolve any aliases first
	resolvedID := ResolveAlias(modelID)
	return resolvedID
}

func stripAnthropicDateSuffix(modelID string) (string, bool) {
	// Anthropic's canonical model ids often include a date suffix (YYYYMMDD), e.g.
	// "anthropic/claude-opus-4-20250514". Gateways like OpenRouter typically
	// expect the non-dated id (e.g. "anthropic/claude-opus-4").
	//
	// Only strip when the id:
	// - is anthropic/claude-...
	// - ends in "-<8 digits>"
	lower := strings.ToLower(strings.TrimSpace(modelID))
	if !strings.HasPrefix(lower, "anthropic/claude-") {
		return "", false
	}
	if len(lower) < 9 {
		return "", false
	}
	suffix := lower[len(lower)-9:]
	if suffix[0] != '-' {
		return "", false
	}
	for i := 1; i < len(suffix); i++ {
		if suffix[i] < '0' || suffix[i] > '9' {
			return "", false
		}
	}
	return lower[:len(lower)-9], true
}

// ResolveAlias returns the actual model ID for a given alias.
// If the input is not an alias, it returns the input unchanged.
func ResolveAlias(modelID string) string {
	normalized := strings.TrimSpace(modelID)
	if normalized == "" {
		return normalized
	}
	if resolved, ok := OpenClawAliases[normalized]; ok {
		return resolved
	}
	lower := strings.ToLower(normalized)
	if resolved, ok := OpenClawAliases[lower]; ok {
		return resolved
	}
	if stripped, ok := stripAnthropicDateSuffix(lower); ok {
		// If the stripped id itself is an alias, resolve it too.
		if resolved, ok := OpenClawAliases[stripped]; ok {
			return resolved
		}
		return stripped
	}
	return normalized
}
