package connector

import (
	"strings"
)

// ModelBackend identifies which backend to use for a model
// All backends use the OpenAI SDK with different base URLs
type ModelBackend string

const (
	BackendOpenAI     ModelBackend = "openai"
	BackendOpenRouter ModelBackend = "openrouter"
)

// Default models for each provider
const (
	DefaultModelOpenAI     = "openai/gpt-5.2"
	DefaultModelOpenRouter = "anthropic/claude-opus-4.5"
	DefaultModelBeeper     = "anthropic/claude-opus-4.5"
)

// ParseModelPrefix extracts the backend and actual model ID from a prefixed model
// Examples:
//   - "openai/gpt-5.2" → (BackendOpenAI, "gpt-5.2")
//   - "anthropic/claude-sonnet-4.5" (no routing prefix) → ("", "anthropic/claude-sonnet-4.5")
//   - "gpt-4o" (no prefix) → ("", "gpt-4o")
func ParseModelPrefix(modelID string) (backend ModelBackend, actualModel string) {
	parts := strings.SplitN(modelID, "/", 2)
	if len(parts) != 2 {
		return "", modelID // No prefix, return as-is
	}

	switch parts[0] {
	case "openai":
		return BackendOpenAI, parts[1]
	case "openrouter":
		return BackendOpenRouter, parts[1] // parts[1] = "openai/gpt-5" (nested)
	default:
		return "", modelID // Unknown prefix, return as-is
	}
}

// HasValidPrefix checks if a model ID has a valid backend prefix
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

// DefaultModelForProvider returns the default model for a given provider
func DefaultModelForProvider(provider string) string {
	switch provider {
	case ProviderOpenAI:
		return DefaultModelOpenAI
	case ProviderOpenRouter:
		return DefaultModelOpenRouter
	case ProviderBeeper:
		return DefaultModelBeeper
	default:
		return DefaultModelOpenRouter
	}
}

// FormatModelDisplay formats a prefixed model ID for display.
func FormatModelDisplay(modelID string) string {
	return GetModelDisplayName(modelID)
}
