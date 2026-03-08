package connector

import (
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
)

func TestEffectiveTemperatureDefaultUnset(t *testing.T) {
	client := &AIClient{}
	if got := client.effectiveTemperature(nil); got != 0 {
		t.Fatalf("expected default temperature 0 (unset), got %v", got)
	}
}

func TestDefaultThinkLevelModelAware(t *testing.T) {
	client := &AIClient{
		connector: &OpenAIConnector{},
		UserLogin: &bridgev2.UserLogin{UserLogin: &database.UserLogin{Metadata: &UserLoginMetadata{
			Provider: ProviderOpenRouter,
			ModelCache: &ModelCache{Models: []ModelInfo{
				{ID: "openai/o4-mini", SupportsReasoning: true},
				{ID: "openai/gpt-4o-mini", SupportsReasoning: false},
			}},
		}}},
	}

	reasoningMeta := &PortalMetadata{
		ResolvedTarget: &ResolvedTarget{
			Kind:    ResolvedTargetModel,
			GhostID: modelUserID("openai/o4-mini"),
			ModelID: "openai/o4-mini",
		},
	}
	if got := client.defaultThinkLevel(reasoningMeta); got != "low" {
		t.Fatalf("expected low for reasoning-capable models, got %q", got)
	}

	nonReasoningMeta := &PortalMetadata{
		ResolvedTarget: &ResolvedTarget{
			Kind:    ResolvedTargetModel,
			GhostID: modelUserID("openai/gpt-4o-mini"),
			ModelID: "openai/gpt-4o-mini",
		},
	}
	if got := client.defaultThinkLevel(nonReasoningMeta); got != "off" {
		t.Fatalf("expected off for non-reasoning models, got %q", got)
	}
}
