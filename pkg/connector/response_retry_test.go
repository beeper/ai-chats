package connector

import (
	"testing"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

func newCompactorTestClient(pruning *PruningConfig, provider string) *AIClient {
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

func TestGetCompactor_UsesPruningCompactionFields(t *testing.T) {
	enabled := false
	pruning := &PruningConfig{
		Enabled:                true,
		SummarizationEnabled:   &enabled,
		SummarizationModel:     "openai/gpt-test",
		MaxSummaryTokens:       321,
		MaxHistoryShare:        0.42,
		ReserveTokens:          777,
		CustomInstructions:     "preserve TODOs and constraints",
		IdentifierPolicy:       "custom",
		IdentifierInstructions: "Keep ticket IDs untouched.",
	}

	client := newCompactorTestClient(pruning, ProviderOpenAI)
	compactor := client.getCompactor()

	if compactor.config.PruningConfig != pruning {
		t.Fatalf("expected compactor to use the pruning config pointer")
	}
	if compactor.config.SummarizationEnabled == nil || *compactor.config.SummarizationEnabled {
		t.Fatalf("expected summarization_enabled=false to be preserved")
	}
	if compactor.config.SummarizationModel != "openai/gpt-test" {
		t.Fatalf("unexpected summarization model: %q", compactor.config.SummarizationModel)
	}
	if compactor.config.MaxSummaryTokens != 321 {
		t.Fatalf("unexpected max summary tokens: %d", compactor.config.MaxSummaryTokens)
	}
	if compactor.config.MaxHistoryShare != 0.42 {
		t.Fatalf("unexpected max history share: %f", compactor.config.MaxHistoryShare)
	}
	if compactor.config.ReserveTokens != 777 {
		t.Fatalf("unexpected reserve tokens: %d", compactor.config.ReserveTokens)
	}
	if compactor.config.CustomInstructions != "preserve TODOs and constraints" {
		t.Fatalf("unexpected custom instructions: %q", compactor.config.CustomInstructions)
	}
	if compactor.config.IdentifierPolicy != "custom" {
		t.Fatalf("unexpected identifier policy: %q", compactor.config.IdentifierPolicy)
	}
	if compactor.config.IdentifierInstructions != "Keep ticket IDs untouched." {
		t.Fatalf("unexpected identifier instructions: %q", compactor.config.IdentifierInstructions)
	}
}

func TestGetCompactor_OpenRouterUsesProviderOverrideWhenModelUnset(t *testing.T) {
	client := newCompactorTestClient(&PruningConfig{Enabled: true}, ProviderOpenRouter)
	compactor := client.getCompactor()

	if compactor.summarizationModel != "anthropic/claude-opus-4.6" {
		t.Fatalf("expected openrouter override model, got %q", compactor.summarizationModel)
	}
}

func TestGetCompactor_OpenRouterExplicitModelBeatsProviderOverride(t *testing.T) {
	client := newCompactorTestClient(&PruningConfig{
		Enabled:            true,
		SummarizationModel: "custom/summary-model",
	}, ProviderOpenRouter)
	compactor := client.getCompactor()

	if compactor.config.SummarizationModel != "custom/summary-model" {
		t.Fatalf("expected explicit summarization model, got %q", compactor.config.SummarizationModel)
	}
	if compactor.summarizationModel != "" {
		t.Fatalf("expected provider override to be skipped when explicit model is configured, got %q", compactor.summarizationModel)
	}
}
