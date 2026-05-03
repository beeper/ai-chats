package ai

import (
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestInboundConfig_WithDefaults_Nil(t *testing.T) {
	var cfg *InboundConfig
	result := cfg.WithDefaults()

	if result == nil {
		t.Fatal("WithDefaults should return non-nil config")
	}
	if result.DedupeTTL != DefaultDedupeTTL {
		t.Errorf("Expected DedupeTTL %v, got %v", DefaultDedupeTTL, result.DedupeTTL)
	}
	if result.DedupeMaxSize != DefaultDedupeMaxSize {
		t.Errorf("Expected DedupeMaxSize %d, got %d", DefaultDedupeMaxSize, result.DedupeMaxSize)
	}
	if result.DefaultDebounceMs != DefaultDebounceMs {
		t.Errorf("Expected DefaultDebounceMs %d, got %d", DefaultDebounceMs, result.DefaultDebounceMs)
	}
}

func TestInboundConfig_WithDefaults_Empty(t *testing.T) {
	cfg := &InboundConfig{}
	result := cfg.WithDefaults()

	if result.DedupeTTL != DefaultDedupeTTL {
		t.Errorf("Expected DedupeTTL %v, got %v", DefaultDedupeTTL, result.DedupeTTL)
	}
	if result.DedupeMaxSize != DefaultDedupeMaxSize {
		t.Errorf("Expected DedupeMaxSize %d, got %d", DefaultDedupeMaxSize, result.DedupeMaxSize)
	}
	if result.DefaultDebounceMs != DefaultDebounceMs {
		t.Errorf("Expected DefaultDebounceMs %d, got %d", DefaultDebounceMs, result.DefaultDebounceMs)
	}
}

func TestInboundConfig_WithDefaults_CustomValues(t *testing.T) {
	cfg := &InboundConfig{
		DedupeTTL:         10 * time.Minute,
		DedupeMaxSize:     1000,
		DefaultDebounceMs: 250,
	}
	result := cfg.WithDefaults()

	if result.DedupeTTL != 10*time.Minute {
		t.Errorf("Expected custom DedupeTTL 10m, got %v", result.DedupeTTL)
	}
	if result.DedupeMaxSize != 1000 {
		t.Errorf("Expected custom DedupeMaxSize 1000, got %d", result.DedupeMaxSize)
	}
	if result.DefaultDebounceMs != 250 {
		t.Errorf("Expected custom DefaultDebounceMs 250, got %d", result.DefaultDebounceMs)
	}
}

func TestInboundConfig_WithDefaults_PartialValues(t *testing.T) {
	cfg := &InboundConfig{
		DedupeTTL: 30 * time.Minute,
	}
	result := cfg.WithDefaults()

	if result.DedupeTTL != 30*time.Minute {
		t.Errorf("Expected custom DedupeTTL 30m, got %v", result.DedupeTTL)
	}
	if result.DedupeMaxSize != DefaultDedupeMaxSize {
		t.Errorf("Expected default DedupeMaxSize %d, got %d", DefaultDedupeMaxSize, result.DedupeMaxSize)
	}
	if result.DefaultDebounceMs != DefaultDebounceMs {
		t.Errorf("Expected default DefaultDebounceMs %d, got %d", DefaultDebounceMs, result.DefaultDebounceMs)
	}
}

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
