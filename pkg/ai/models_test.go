package ai

import "testing"

func TestSupportsXhigh(t *testing.T) {
	anthropicOpus := Model{
		ID:       "claude-opus-4-6",
		API:      APIAnthropicMessages,
		Provider: "anthropic",
	}
	if !SupportsXhigh(anthropicOpus) {
		t.Fatalf("expected anthropic opus 4.6 to support xhigh")
	}

	anthropicSonnet := Model{
		ID:       "claude-sonnet-4-5",
		API:      APIAnthropicMessages,
		Provider: "anthropic",
	}
	if SupportsXhigh(anthropicSonnet) {
		t.Fatalf("expected anthropic sonnet 4.5 not to support xhigh")
	}

	openRouterOpus := Model{
		ID:       "anthropic/claude-opus-4.6",
		API:      APIOpenAICompletions,
		Provider: "openrouter",
	}
	if SupportsXhigh(openRouterOpus) {
		t.Fatalf("expected openrouter opus to not support xhigh")
	}
}

func TestModelsAreEqual(t *testing.T) {
	a := &Model{ID: "gpt-4o", Provider: "openai"}
	b := &Model{ID: "gpt-4o", Provider: "openai"}
	if !ModelsAreEqual(a, b) {
		t.Fatalf("expected models to be equal")
	}
	if ModelsAreEqual(a, nil) {
		t.Fatalf("expected nil model comparison to be false")
	}
}

func TestModelRegistryDeterministicOrdering(t *testing.T) {
	previous := modelRegistry
	modelRegistry = map[string]map[string]Model{}
	defer func() {
		modelRegistry = previous
	}()

	RegisterModels("z-provider", []Model{{ID: "b-model"}, {ID: "a-model"}})
	RegisterModels("a-provider", []Model{{ID: "z-model"}, {ID: "x-model"}})

	providers := GetProviders()
	if len(providers) != 2 || providers[0] != "a-provider" || providers[1] != "z-provider" {
		t.Fatalf("expected sorted providers, got %#v", providers)
	}

	models := GetModels("z-provider")
	if len(models) != 2 || models[0].ID != "a-model" || models[1].ID != "b-model" {
		t.Fatalf("expected sorted model IDs, got %#v", models)
	}
}
