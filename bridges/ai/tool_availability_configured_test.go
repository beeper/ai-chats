package ai

import (
	"context"
	"runtime"
	"strings"
	"testing"

	"github.com/beeper/agentremote/pkg/shared/toolspec"
)

func boolPtr(v bool) *bool {
	return &v
}

func TestToolAvailable_WebSearch_RequiresAnyProviderKey(t *testing.T) {
	oc := newTestAIClientWithProvider("")
	oc.connector = &OpenAIConnector{
		Config: Config{
			Tools: ToolProvidersConfig{
				Web: &WebToolsConfig{Search: &SearchConfig{}},
			},
		},
	}
	setTestLoginState(oc, &loginRuntimeState{
		ModelCache: &ModelCache{Models: []ModelInfo{{ID: "openai/gpt-5.2", SupportsToolCalling: true}}},
	})
	meta := modelModeTestMeta("openai/gpt-5.2")

	ok, source, reason := oc.isToolAvailable(meta, toolspec.WebSearchName)
	if ok {
		t.Fatalf("expected web_search to be unavailable without provider keys")
	}
	if source != SourceProviderLimit {
		t.Fatalf("expected SourceProviderLimit, got %q (reason=%q)", source, reason)
	}
	if !strings.Contains(strings.ToLower(reason), "not configured") {
		t.Fatalf("expected a configuration-related reason, got %q", reason)
	}
}

func TestToolAvailable_WebSearch_WithProviderKey(t *testing.T) {
	oc := newTestAIClientWithProvider("")
	oc.connector = &OpenAIConnector{
		Config: Config{
			Tools: ToolProvidersConfig{
				Web: &WebToolsConfig{Search: &SearchConfig{
					Exa: ProviderExaConfig{APIKey: "test"},
				}},
			},
		},
	}
	setTestLoginState(oc, &loginRuntimeState{
		ModelCache: &ModelCache{Models: []ModelInfo{{ID: "openai/gpt-5.2", SupportsToolCalling: true}}},
	})
	meta := modelModeTestMeta("openai/gpt-5.2")

	ok, _, reason := oc.isToolAvailable(meta, toolspec.WebSearchName)
	if !ok {
		t.Fatalf("expected web_search to be available, got reason=%q", reason)
	}
}

func TestToolAvailable_WebFetch_DirectDisabledAndNoExaKey(t *testing.T) {
	oc := newTestAIClientWithProvider("")
	oc.connector = &OpenAIConnector{
		Config: Config{
			Tools: ToolProvidersConfig{
				Web: &WebToolsConfig{Fetch: &FetchConfig{
					Direct: ProviderDirectConfig{Enabled: boolPtr(false)},
				}},
			},
		},
	}
	setTestLoginState(oc, &loginRuntimeState{
		ModelCache: &ModelCache{Models: []ModelInfo{{ID: "openai/gpt-5.2", SupportsToolCalling: true}}},
	})
	meta := modelModeTestMeta("openai/gpt-5.2")

	ok, source, reason := oc.isToolAvailable(meta, toolspec.WebFetchName)
	if ok {
		t.Fatalf("expected web_fetch to be unavailable when direct is disabled and no Exa key")
	}
	if source != SourceProviderLimit {
		t.Fatalf("expected SourceProviderLimit, got %q (reason=%q)", source, reason)
	}
}

func TestToolAvailable_TTS_PlatformBehavior(t *testing.T) {
	oc := newTestAIClientWithProvider("")
	oc.connector = &OpenAIConnector{Config: Config{}}
	setTestLoginState(oc, &loginRuntimeState{
		ModelCache: &ModelCache{Models: []ModelInfo{{ID: "openai/gpt-5.2", SupportsToolCalling: true}}},
	})
	meta := modelModeTestMeta("openai/gpt-5.2")

	ok, _, reason := oc.isToolAvailable(meta, toolspec.TTSName)
	if runtime.GOOS == "darwin" {
		if !ok {
			t.Fatalf("expected TTS to be available on macOS via say, got reason=%q", reason)
		}
		return
	}
	if ok {
		t.Fatalf("expected TTS to be unavailable without configured provider on non-macOS")
	}
}

func TestEffectiveSearchConfig_UsesEnvDefaultsWithoutPanicking(t *testing.T) {
	// Basic sanity check that we can always compute an effective config.
	oc := &AIClient{connector: &OpenAIConnector{Config: Config{}}}
	cfg := oc.effectiveSearchConfig(context.Background())
	if cfg == nil {
		t.Fatalf("expected non-nil config")
	}
}

func TestEffectiveSearchConfig_UsesEnvWhenConfigMissing(t *testing.T) {
	t.Setenv("SEARCH_PROVIDER", "exa")
	t.Setenv("SEARCH_FALLBACKS", "exa")
	t.Setenv("EXA_API_KEY", "env-exa-key")
	t.Setenv("EXA_BASE_URL", "https://exa-proxy.example")

	oc := &AIClient{connector: &OpenAIConnector{Config: Config{}}}
	cfg := oc.effectiveSearchConfig(context.Background())
	if cfg == nil {
		t.Fatalf("expected non-nil config")
	}
	if cfg.Provider != "exa" {
		t.Fatalf("expected env provider, got %q", cfg.Provider)
	}
	if len(cfg.Fallbacks) != 1 || cfg.Fallbacks[0] != "exa" {
		t.Fatalf("expected env fallbacks, got %#v", cfg.Fallbacks)
	}
	if cfg.Exa.APIKey != "env-exa-key" {
		t.Fatalf("expected env Exa API key, got %q", cfg.Exa.APIKey)
	}
	if cfg.Exa.BaseURL != "https://exa-proxy.example" {
		t.Fatalf("expected env Exa base URL, got %q", cfg.Exa.BaseURL)
	}
}

func TestEffectiveFetchConfig_UsesEnvWhenConfigMissing(t *testing.T) {
	t.Setenv("FETCH_PROVIDER", "direct")
	t.Setenv("FETCH_FALLBACKS", "direct,exa")
	t.Setenv("EXA_API_KEY", "env-exa-key")
	t.Setenv("EXA_BASE_URL", "https://exa-proxy.example")

	oc := &AIClient{connector: &OpenAIConnector{Config: Config{}}}
	cfg := oc.effectiveFetchConfig(context.Background())
	if cfg == nil {
		t.Fatalf("expected non-nil config")
	}
	if cfg.Provider != "direct" {
		t.Fatalf("expected env provider, got %q", cfg.Provider)
	}
	if len(cfg.Fallbacks) != 2 || cfg.Fallbacks[0] != "direct" || cfg.Fallbacks[1] != "exa" {
		t.Fatalf("expected env fallbacks, got %#v", cfg.Fallbacks)
	}
	if cfg.Exa.APIKey != "env-exa-key" {
		t.Fatalf("expected env Exa API key, got %q", cfg.Exa.APIKey)
	}
	if cfg.Exa.BaseURL != "https://exa-proxy.example" {
		t.Fatalf("expected env Exa base URL, got %q", cfg.Exa.BaseURL)
	}
	if cfg.Direct.TimeoutSecs == 0 {
		t.Fatalf("expected fetch defaults to remain applied")
	}
}
