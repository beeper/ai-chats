package ai

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestModelsConfigProviderMatchesNormalizedKeys(t *testing.T) {
	var cfg ModelsConfig
	if err := yaml.Unmarshal([]byte(`
mode: merge
providers:
  " OpenAI ":
    api_key: tok
`), &cfg); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}

	got := cfg.Provider("openai")
	if got.APIKey != "tok" {
		t.Fatalf("expected normalized provider lookup to match, got %#v", got)
	}
}

func TestModelsConfigUnmarshalRejectsNormalizedKeyCollisions(t *testing.T) {
	var cfg ModelsConfig
	err := yaml.Unmarshal([]byte(`
mode: merge
providers:
  OpenAI:
    api_key: tok-1
  " openai ":
    api_key: tok-2
`), &cfg)
	if err == nil {
		t.Fatal("expected duplicate normalized provider keys to fail")
	}
	if !strings.Contains(err.Error(), "duplicate provider key") {
		t.Fatalf("unexpected error: %v", err)
	}
}
