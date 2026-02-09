package connector

import (
	"context"
	"runtime"
	"strings"
	"testing"

	"github.com/beeper/ai-bridge/pkg/shared/toolspec"
)

func TestToolAvailable_WebSearch_RequiresAnyProviderKey(t *testing.T) {
	oc := &AIClient{
		connector: &OpenAIConnector{
			Config: Config{
				Tools: ToolProvidersConfig{
					Search: &SearchConfig{},
				},
			},
		},
	}
	meta := &PortalMetadata{Capabilities: ModelCapabilities{SupportsToolCalling: true}}

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
						Brave: ProviderBraveConfig{APIKey: "test"},
					},
				},
			},
		},
	}
	meta := &PortalMetadata{Capabilities: ModelCapabilities{SupportsToolCalling: true}}

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
	}
	meta := &PortalMetadata{Capabilities: ModelCapabilities{SupportsToolCalling: true}}

	ok, source, reason := oc.isToolAvailable(meta, toolspec.WebFetchName)
	if ok {
		t.Fatalf("expected web_fetch to be unavailable when direct is disabled and no Exa key")
	}
	if source != SourceProviderLimit {
		t.Fatalf("expected SourceProviderLimit, got %q (reason=%q)", source, reason)
	}
}

func TestToolAvailable_Cron_RequiresService(t *testing.T) {
	oc := &AIClient{
		connector: &OpenAIConnector{Config: Config{}},
		// cronService intentionally nil
	}
	meta := &PortalMetadata{Capabilities: ModelCapabilities{SupportsToolCalling: true}}

	ok, _, reason := oc.isToolAvailable(meta, toolspec.CronName)
	if ok {
		t.Fatalf("expected cron to be unavailable without cronService")
	}
	if strings.TrimSpace(reason) == "" {
		t.Fatalf("expected non-empty reason")
	}
}

func TestToolAvailable_TTS_PlatformBehavior(t *testing.T) {
	oc := &AIClient{
		connector: &OpenAIConnector{Config: Config{}},
		// provider/apiKey intentionally empty
	}
	meta := &PortalMetadata{Capabilities: ModelCapabilities{SupportsToolCalling: true}}

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

func TestToolAvailable_MemorySearch_DisabledByConfig(t *testing.T) {
	oc := &AIClient{
		connector: &OpenAIConnector{
			Config: Config{
				MemorySearch: &MemorySearchConfig{Enabled: boolPtr(false)},
			},
		},
	}
	meta := &PortalMetadata{Capabilities: ModelCapabilities{SupportsToolCalling: true}}

	ok, _, reason := oc.isToolAvailable(meta, toolspec.MemorySearchName)
	if ok {
		t.Fatalf("expected memory_search to be unavailable when explicitly disabled")
	}
	if strings.TrimSpace(reason) == "" {
		t.Fatalf("expected non-empty reason")
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
