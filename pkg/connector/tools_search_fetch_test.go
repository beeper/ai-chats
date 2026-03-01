package connector

import (
	"testing"

	"github.com/beeper/ai-bridge/pkg/search"
)

func TestApplyLoginTokensToSearchConfig_MagicProxyForcesExa(t *testing.T) {
	oc := &OpenAIConnector{}
	meta := &UserLoginMetadata{
		Provider: ProviderMagicProxy,
		APIKey:   "magic-token",
		BaseURL:  "https://bai.bt.hn/team/proxy",
	}
	cfg := &search.Config{
		Provider:  search.ProviderExa,
		Fallbacks: []string{search.ProviderExa},
	}

	got := applyLoginTokensToSearchConfig(cfg, meta, oc)

	if got.Provider != search.ProviderExa {
		t.Fatalf("expected provider %q, got %q", search.ProviderExa, got.Provider)
	}
	if len(got.Fallbacks) != 1 || got.Fallbacks[0] != search.ProviderExa {
		t.Fatalf("expected exa-only fallbacks, got %#v", got.Fallbacks)
	}
	if got.Exa.BaseURL != "https://bai.bt.hn/team/proxy/exa" {
		t.Fatalf("unexpected exa base URL: %q", got.Exa.BaseURL)
	}
	if got.Exa.APIKey != "magic-token" {
		t.Fatalf("unexpected exa API key: %q", got.Exa.APIKey)
	}
}

func TestApplyLoginTokensToSearchConfig_CustomExaEndpointForcesExa(t *testing.T) {
	oc := &OpenAIConnector{}
	meta := &UserLoginMetadata{Provider: ProviderOpenAI}
	cfg := &search.Config{
		Provider:  search.ProviderExa,
		Fallbacks: []string{search.ProviderExa},
		Exa: search.ExaConfig{
			APIKey:  "exa-token",
			BaseURL: "https://ai.bt.hn/exa",
		},
	}

	got := applyLoginTokensToSearchConfig(cfg, meta, oc)

	if got.Provider != search.ProviderExa {
		t.Fatalf("expected provider %q, got %q", search.ProviderExa, got.Provider)
	}
	if len(got.Fallbacks) != 1 || got.Fallbacks[0] != search.ProviderExa {
		t.Fatalf("expected exa-only fallbacks, got %#v", got.Fallbacks)
	}
}

func TestApplyLoginTokensToSearchConfig_DefaultExaEndpointDoesNotForceExa(t *testing.T) {
	oc := &OpenAIConnector{}
	meta := &UserLoginMetadata{
		Provider: ProviderOpenRouter,
		APIKey:   "openrouter-token",
	}
	cfg := &search.Config{
		Provider:  search.ProviderExa,
		Fallbacks: []string{search.ProviderExa},
		Exa: search.ExaConfig{
			BaseURL: "https://api.exa.ai",
		},
	}

	got := applyLoginTokensToSearchConfig(cfg, meta, oc)

	if got.Provider != search.ProviderExa {
		t.Fatalf("unexpected provider override: %q", got.Provider)
	}
	if len(got.Fallbacks) != 1 || got.Fallbacks[0] != search.ProviderExa {
		t.Fatalf("unexpected fallbacks: %#v", got.Fallbacks)
	}
	if got.Exa.APIKey == "openrouter-token" {
		t.Fatalf("openrouter token must not be copied into exa api key")
	}
}
