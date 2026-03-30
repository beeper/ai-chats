package ai

import "testing"

func TestModelsConfigProviderMatchesNormalizedKeys(t *testing.T) {
	cfg := &ModelsConfig{
		Providers: map[string]ModelProviderConfig{
			" OpenAI ": {APIKey: "tok"},
		},
	}

	got := cfg.Provider("openai")
	if got.APIKey != "tok" {
		t.Fatalf("expected normalized provider lookup to match, got %#v", got)
	}
}
