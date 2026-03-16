package ai

import (
	"testing"

	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
)

func TestEffectiveTemperatureDefaultUnset(t *testing.T) {
	client := &AIClient{}
	if got := client.effectiveTemperature(nil); got != nil {
		t.Fatalf("expected default temperature to be unset, got %v", *got)
	}
}

func TestEffectiveTemperatureUsesExplicitAgentZero(t *testing.T) {
	client := &AIClient{
		connector: &OpenAIConnector{},
		UserLogin: &bridgev2.UserLogin{UserLogin: &database.UserLogin{Metadata: &UserLoginMetadata{
			CustomAgents: map[string]*AgentDefinitionContent{
				"agent-1": {
					ID:          "agent-1",
					Name:        "Agent One",
					Model:       "openai/gpt-5.2",
					Temperature: ptr.Ptr(0.0),
				},
			},
		}}},
	}
	meta := &PortalMetadata{
		ResolvedTarget: &ResolvedTarget{
			Kind:    ResolvedTargetAgent,
			AgentID: "agent-1",
		},
	}

	got := client.effectiveTemperature(meta)
	if got == nil || *got != 0 {
		t.Fatalf("expected explicit zero temperature, got %#v", got)
	}
}

func TestEffectiveTemperatureUsesExplicitNonZero(t *testing.T) {
	client := &AIClient{
		connector: &OpenAIConnector{},
		UserLogin: &bridgev2.UserLogin{UserLogin: &database.UserLogin{Metadata: &UserLoginMetadata{
			CustomAgents: map[string]*AgentDefinitionContent{
				"agent-1": {
					ID:          "agent-1",
					Name:        "Agent One",
					Model:       "openai/gpt-5.2",
					Temperature: ptr.Ptr(0.7),
				},
			},
		}}},
	}
	meta := &PortalMetadata{
		ResolvedTarget: &ResolvedTarget{
			Kind:    ResolvedTargetAgent,
			AgentID: "agent-1",
		},
	}

	got := client.effectiveTemperature(meta)
	if got == nil || *got != 0.7 {
		t.Fatalf("expected explicit non-zero temperature, got %#v", got)
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
