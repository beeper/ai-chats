package ai

import "strings"

// ModelBackend identifies which backend to use for a model
// All backends use the OpenAI SDK with different base URLs
type ModelBackend string

const (
	BackendOpenAI     ModelBackend = "openai"
	BackendOpenRouter ModelBackend = "openrouter"
)

// Default models for each provider
const (
	DefaultModelOpenAI = "openai/gpt-5.2"
	// OpenRouter-compatible backends (OpenRouter + Magic Proxy) should default to Opus.
	DefaultModelOpenRouter = "anthropic/claude-opus-4.6"
)

// ParseModelPrefix extracts the backend and actual model ID from a prefixed model
// Examples:
//   - "openai/gpt-5.2" → (BackendOpenAI, "gpt-5.2")
//   - "anthropic/claude-sonnet-4.5" (no routing prefix) → ("", "anthropic/claude-sonnet-4.5")
//   - "gpt-4.1" (no prefix) → ("", "gpt-4.1")
func ParseModelPrefix(modelID string) (backend ModelBackend, actualModel string) {
	prefix, rest, ok := strings.Cut(modelID, "/")
	if !ok {
		return "", modelID // No prefix, return as-is
	}

	switch prefix {
	case "openai":
		return BackendOpenAI, rest
	case "openrouter":
		return BackendOpenRouter, rest // rest = "openai/gpt-5" (nested)
	default:
		return "", modelID // Unknown prefix, return as-is
	}
}

func splitModelProvider(modelID string) (string, string) {
	trimmed := strings.TrimSpace(modelID)
	if trimmed == "" {
		return "", ""
	}
	provider, model, ok := strings.Cut(trimmed, "/")
	if !ok {
		return "", trimmed
	}
	return strings.ToLower(strings.TrimSpace(provider)), strings.TrimSpace(model)
}

func HasValidPrefix(modelID string) bool {
	backend, _ := ParseModelPrefix(modelID)
	return backend != ""
}

// AddModelPrefix adds a prefix to a model ID if it doesn't have one
func AddModelPrefix(backend ModelBackend, modelID string) string {
	if HasValidPrefix(modelID) {
		return modelID
	}
	return string(backend) + "/" + modelID
}
