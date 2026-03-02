package connector

import (
	"testing"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"

	airuntime "github.com/beeper/ai-bridge/pkg/runtime"
)

func newPruningTestClient(pruning *airuntime.PruningConfig, provider string) *AIClient {
	login := &database.UserLogin{
		ID:       networkid.UserLoginID("login"),
		Metadata: &UserLoginMetadata{Provider: provider},
	}
	return &AIClient{
		UserLogin: &bridgev2.UserLogin{
			UserLogin: login,
			Log:       zerolog.Nop(),
		},
		connector: &OpenAIConnector{
			Config: Config{
				Pruning: pruning,
			},
		},
		log: zerolog.Nop(),
	}
}

func TestPruningReserveTokens_UsesConfigValue(t *testing.T) {
	client := newPruningTestClient(&airuntime.PruningConfig{ReserveTokens: 777}, ProviderOpenAI)
	if got := client.pruningReserveTokens(); got != 777 {
		t.Fatalf("expected reserve tokens 777, got %d", got)
	}
}

func TestPruningReserveTokens_DefaultsWhenUnset(t *testing.T) {
	client := newPruningTestClient(&airuntime.PruningConfig{}, ProviderOpenAI)
	if got := client.pruningReserveTokens(); got != 20000 {
		t.Fatalf("expected default reserve tokens 20000, got %d", got)
	}
}

func TestPruningReserveTokens_RespectsFloor(t *testing.T) {
	client := newPruningTestClient(&airuntime.PruningConfig{
		ReserveTokens:      4000,
		ReserveTokensFloor: 9000,
	}, ProviderOpenAI)
	if got := client.pruningReserveTokens(); got != 9000 {
		t.Fatalf("expected reserve floor 9000, got %d", got)
	}
}

func TestPruningOverflowFlushConfig_ReadsFromPruning(t *testing.T) {
	enabled := true
	client := newPruningTestClient(&airuntime.PruningConfig{
		OverflowFlush: &airuntime.OverflowFlushConfig{
			Enabled:             &enabled,
			SoftThresholdTokens: 1234,
			Prompt:              "flush",
			SystemPrompt:        "sys",
		},
	}, ProviderOpenAI)
	cfg := client.pruningOverflowFlushConfig()
	if cfg == nil {
		t.Fatal("expected overflow flush config")
	}
	if cfg.Enabled == nil || !*cfg.Enabled {
		t.Fatal("expected overflow flush enabled")
	}
	if cfg.SoftThresholdTokens != 1234 {
		t.Fatalf("expected threshold 1234, got %d", cfg.SoftThresholdTokens)
	}
	if cfg.Prompt != "flush" || cfg.SystemPrompt != "sys" {
		t.Fatalf("unexpected prompts: %#v", cfg)
	}
}

func TestPruningMaxHistoryShare_DefaultsWhenUnset(t *testing.T) {
	client := newPruningTestClient(&airuntime.PruningConfig{}, ProviderOpenAI)
	if got := client.pruningMaxHistoryShare(); got != 0.5 {
		t.Fatalf("expected default max history share 0.5, got %v", got)
	}
}

func TestPruningMaxHistoryShare_UsesConfigValue(t *testing.T) {
	client := newPruningTestClient(&airuntime.PruningConfig{
		MaxHistoryShare: 0.35,
	}, ProviderOpenAI)
	if got := client.pruningMaxHistoryShare(); got != 0.35 {
		t.Fatalf("expected max history share 0.35, got %v", got)
	}
}

func TestPruningCompactionMode_DefaultsWhenUnset(t *testing.T) {
	client := newPruningTestClient(&airuntime.PruningConfig{}, ProviderOpenAI)
	if got := client.pruningCompactionMode(); got != "safeguard" {
		t.Fatalf("expected default compaction mode safeguard, got %q", got)
	}
}

func TestPruningCompactionMode_NormalizesInvalid(t *testing.T) {
	client := newPruningTestClient(&airuntime.PruningConfig{CompactionMode: "weird"}, ProviderOpenAI)
	if got := client.pruningCompactionMode(); got != "safeguard" {
		t.Fatalf("expected invalid compaction mode to fallback safeguard, got %q", got)
	}
}

func TestPruningKeepRecentTokens_DefaultsWhenUnset(t *testing.T) {
	client := newPruningTestClient(&airuntime.PruningConfig{}, ProviderOpenAI)
	if got := client.pruningKeepRecentTokens(); got != 20000 {
		t.Fatalf("expected keep recent tokens default 20000, got %d", got)
	}
}

func TestPruningSummarizationSettings_Defaults(t *testing.T) {
	client := newPruningTestClient(&airuntime.PruningConfig{}, ProviderOpenAI)
	if !client.pruningSummarizationEnabled() {
		t.Fatal("expected summarization enabled by default")
	}
	if got := client.pruningMaxSummaryTokens(); got != 500 {
		t.Fatalf("expected default max summary tokens 500, got %d", got)
	}
}

func TestPruningSummarizationModel_Defaults(t *testing.T) {
	client := newPruningTestClient(&airuntime.PruningConfig{}, ProviderOpenAI)
	if got := client.pruningSummarizationModel(); got == "" {
		t.Fatal("expected non-empty summarization model default")
	}
}

func TestPruningIdentifierAndCustomInstructions(t *testing.T) {
	client := newPruningTestClient(&airuntime.PruningConfig{
		CustomInstructions:     "Keep unresolved TODOs.",
		IdentifierPolicy:       "custom",
		IdentifierInstructions: "Keep IDs exact.",
	}, ProviderOpenAI)
	if got := client.pruningCustomInstructions(); got != "Keep unresolved TODOs." {
		t.Fatalf("unexpected custom instructions: %q", got)
	}
	if got := client.pruningIdentifierPolicy(); got != "custom" {
		t.Fatalf("unexpected identifier policy: %q", got)
	}
	if got := client.pruningIdentifierInstructions(); got != "Keep IDs exact." {
		t.Fatalf("unexpected identifier instructions: %q", got)
	}
}

func TestProjectedCompactionFlushTokens(t *testing.T) {
	meta := &PortalMetadata{
		ModuleMeta: map[string]any{
			"compaction_last_prompt_tokens":     int64(5000),
			"compaction_last_completion_tokens": int64(1200),
		},
	}
	if got := projectedCompactionFlushTokens(meta, 600); got != 6800 {
		t.Fatalf("expected projected flush tokens 6800, got %d", got)
	}
	if got := projectedCompactionFlushTokens(nil, 600); got != 600 {
		t.Fatalf("expected prompt-only fallback 600, got %d", got)
	}
}

func TestPruningPostCompactionRefreshPrompt_Defaults(t *testing.T) {
	client := newPruningTestClient(&airuntime.PruningConfig{}, ProviderOpenAI)
	if got := client.pruningPostCompactionRefreshPrompt(); got == "" {
		t.Fatal("expected non-empty post-compaction refresh prompt")
	}
}
