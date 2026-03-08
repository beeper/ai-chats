package connector

import (
	"context"
	"runtime"
	"strings"
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"

	"github.com/beeper/ai-bridge/pkg/shared/toolspec"
)

func boolPtr(v bool) *bool {
	return &v
}

func TestToolAvailable_WebSearch_RequiresAnyProviderKey(t *testing.T) {
	oc := &AIClient{
		connector: &OpenAIConnector{
			Config: Config{
				Tools: ToolProvidersConfig{
					Search: &SearchConfig{},
				},
			},
		},
		UserLogin: &bridgev2.UserLogin{UserLogin: &database.UserLogin{Metadata: &UserLoginMetadata{
			ModelCache: &ModelCache{Models: []ModelInfo{{ID: "openai/gpt-5.2", SupportsToolCalling: true}}},
		}}},
	}
	meta := simpleModeTestMeta("openai/gpt-5.2")

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
	oc := &AIClient{
		connector: &OpenAIConnector{
			Config: Config{
				Tools: ToolProvidersConfig{
					Search: &SearchConfig{
						Exa: ProviderExaConfig{APIKey: "test"},
					},
				},
			},
		},
		UserLogin: &bridgev2.UserLogin{UserLogin: &database.UserLogin{Metadata: &UserLoginMetadata{
			ModelCache: &ModelCache{Models: []ModelInfo{{ID: "openai/gpt-5.2", SupportsToolCalling: true}}},
		}}},
	}
	meta := simpleModeTestMeta("openai/gpt-5.2")

	ok, _, reason := oc.isToolAvailable(meta, toolspec.WebSearchName)
	if !ok {
		t.Fatalf("expected web_search to be available, got reason=%q", reason)
	}
}

func TestToolAvailable_WebFetch_DirectDisabledAndNoExaKey(t *testing.T) {
	oc := &AIClient{
		connector: &OpenAIConnector{
			Config: Config{
				Tools: ToolProvidersConfig{
					Fetch: &FetchConfig{
						Direct: ProviderDirectConfig{Enabled: boolPtr(false)},
					},
				},
			},
		},
		UserLogin: &bridgev2.UserLogin{UserLogin: &database.UserLogin{Metadata: &UserLoginMetadata{
			ModelCache: &ModelCache{Models: []ModelInfo{{ID: "openai/gpt-5.2", SupportsToolCalling: true}}},
		}}},
	}
	meta := simpleModeTestMeta("openai/gpt-5.2")

	ok, source, reason := oc.isToolAvailable(meta, toolspec.WebFetchName)
	if ok {
		t.Fatalf("expected web_fetch to be unavailable when direct is disabled and no Exa key")
	}
	if source != SourceProviderLimit {
		t.Fatalf("expected SourceProviderLimit, got %q (reason=%q)", source, reason)
	}
}

func TestToolAvailable_TTS_PlatformBehavior(t *testing.T) {
	oc := &AIClient{
		connector: &OpenAIConnector{Config: Config{}},
		// provider/apiKey intentionally empty
		UserLogin: &bridgev2.UserLogin{UserLogin: &database.UserLogin{Metadata: &UserLoginMetadata{
			ModelCache: &ModelCache{Models: []ModelInfo{{ID: "openai/gpt-5.2", SupportsToolCalling: true}}},
		}}},
	}
	meta := simpleModeTestMeta("openai/gpt-5.2")

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
