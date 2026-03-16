package ai

import "testing"

func TestModelManifestMatchesOpenRouterAllowlist(t *testing.T) {
	expected := map[string]struct{}{
		"google/gemini-3.1-flash-lite-preview":  {},
		"openai/gpt-5.3-chat":                   {},
		"openai/gpt-5.4":                        {},
		"qwen/qwen2.5-vl-32b-instruct":          {},
		"qwen/qwen3-32b":                        {},
		"qwen/qwen3-235b-a22b":                  {},
		"qwen/qwen3-coder":                      {},
		"anthropic/claude-sonnet-4":             {},
		"anthropic/claude-sonnet-4.5":           {},
		"anthropic/claude-sonnet-4.6":           {},
		"anthropic/claude-opus-4.1":             {},
		"anthropic/claude-haiku-4.5":            {},
		"anthropic/claude-opus-4.5":             {},
		"anthropic/claude-opus-4.6":             {},
		"deepseek/deepseek-chat-v3-0324":        {},
		"deepseek/deepseek-chat-v3.1":           {},
		"deepseek/deepseek-v3.1-terminus":       {},
		"deepseek/deepseek-v3.2":                {},
		"deepseek/deepseek-r1":                  {},
		"deepseek/deepseek-r1-0528":             {},
		"deepseek/deepseek-r1-distill-qwen-32b": {},
		"google/gemini-2.0-flash-001":           {},
		"google/gemini-2.5-flash":               {},
		"google/gemini-2.5-flash-lite":          {},
		"google/gemini-2.5-flash-image":         {},
		"google/gemini-2.0-flash-lite-001":      {},
		"google/gemini-2.5-pro":                 {},
		"google/gemini-3.1-pro-preview":         {},
		"google/gemini-3-pro-image-preview":     {},
		"google/gemini-3-flash-preview":         {},
		"meta-llama/llama-3.3-70b-instruct":     {},
		"meta-llama/llama-4-scout":              {},
		"meta-llama/llama-4-maverick":           {},
		"minimax/minimax-m2":                    {},
		"minimax/minimax-m2.1":                  {},
		"minimax/minimax-m2.5":                  {},
		"moonshotai/kimi-k2":                    {},
		"moonshotai/kimi-k2-0905":               {},
		"moonshotai/kimi-k2.5":                  {},
		"openai/gpt-oss-20b":                    {},
		"openai/gpt-oss-120b":                   {},
		"openai/gpt-4o-mini":                    {},
		"openai/gpt-4.1":                        {},
		"openai/gpt-4.1-mini":                   {},
		"openai/gpt-4.1-nano":                   {},
		"openai/gpt-5":                          {},
		"openai/gpt-5-mini":                     {},
		"openai/gpt-5-nano":                     {},
		"openai/gpt-5.1":                        {},
		"openai/gpt-5.2":                        {},
		"openai/gpt-5.2-pro":                    {},
		"openai/o3-mini":                        {},
		"openai/o4-mini":                        {},
		"openai/o3":                             {},
		"openai/o3-pro":                         {},
		"openai/gpt-5-image-mini":               {},
		"openai/gpt-5-image":                    {},
		"z-ai/glm-4.5":                          {},
		"z-ai/glm-4.5v":                         {},
		"z-ai/glm-4.5-air":                      {},
		"z-ai/glm-4.6":                          {},
		"z-ai/glm-4.6v":                         {},
		"z-ai/glm-4.7":                          {},
		"z-ai/glm-5":                            {},
		"x-ai/grok-4":                           {},
		"x-ai/grok-3":                           {},
		"x-ai/grok-3-mini":                      {},
		"x-ai/grok-4-fast":                      {},
		"x-ai/grok-4.1-fast":                    {},
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

func TestModelManifestAliasesPointToAllowedModels(t *testing.T) {
	for alias, target := range ModelManifest.Aliases {
		if _, ok := ModelManifest.Models[target]; !ok {
			t.Fatalf("alias %q points to non-allowlisted model %q", alias, target)
		}
	}
}
