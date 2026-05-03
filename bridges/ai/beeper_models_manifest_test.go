package ai

import "testing"

func TestModelManifestMatchesOpenRouterAllowlist(t *testing.T) {
	expected := map[string]struct{}{
		"anthropic/claude-haiku-4.5":    {},
		"anthropic/claude-opus-4.6":     {},
		"anthropic/claude-sonnet-4.6":   {},
		"deepseek/deepseek-r1-0528":     {},
		"deepseek/deepseek-v3.2":        {},
		"google/gemini-2.5-flash-lite":  {},
		"google/gemini-2.5-pro":         {},
		"google/gemini-3-flash-preview": {},
		"google/gemma-2-27b-it":         {},
		"meta-llama/llama-4-maverick":   {},
		"minimax/minimax-m2.7":          {},
		"mistralai/devstral-2512":       {},
		"mistralai/mistral-small-2603":  {},
		"moonshotai/kimi-k2.5":          {},
		"openai/gpt-5-mini":             {},
		"openai/gpt-5.2":                {},
		"openai/gpt-5.3-codex":          {},
		"openai/gpt-5.4":                {},
		"openai/gpt-5.4-mini":           {},
		"openai/o3":                     {},
		"openai/o4-mini":                {},
		"qwen/qwen2.5-vl-32b-instruct":  {},
		"qwen/qwen3-coder-next":         {},
		"qwen/qwen3.5-flash-02-23":      {},
		"qwen/qwen3.5-plus-02-15":       {},
		"x-ai/grok-4.1-fast":            {},
		"x-ai/grok-4.20-beta":           {},
		"x-ai/grok-code-fast-1":         {},
		"z-ai/glm-5-turbo":              {},
	}

	if len(ModelManifest.Models) != len(expected) {
		t.Fatalf("model manifest count = %d, want %d", len(ModelManifest.Models), len(expected))
	}
	for modelID := range expected {
		if _, ok := ModelManifest.Models[modelID]; !ok {
			t.Fatalf("model manifest missing %q", modelID)
		}
	}
	for modelID := range ModelManifest.Models {
		if _, ok := expected[modelID]; !ok {
			t.Fatalf("model manifest contains unexpected model %q", modelID)
		}
	}
}
