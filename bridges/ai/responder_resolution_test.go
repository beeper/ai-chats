package ai

import (
	"context"
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
)

func TestResolveResponderForModelUsesModelCatalog(t *testing.T) {
	client := &AIClient{
		connector: &OpenAIConnector{},
		UserLogin: &bridgev2.UserLogin{UserLogin: &database.UserLogin{Metadata: &UserLoginMetadata{
			ModelCache: &ModelCache{Models: []ModelInfo{{
				ID:              "openai/gpt-5.2",
				Name:            "GPT-5.2",
				ContextWindow:   400000,
				MaxOutputTokens: 16000,
				SupportsVision:  true,
			}}},
		}}},
	}

	responder, err := client.ResolveResponderForModel(context.Background(), "openai/gpt-5.2")
	if err != nil {
		t.Fatalf("ResolveResponderForModel returned error: %v", err)
	}
	if responder == nil {
		t.Fatal("expected responder")
	}
	if responder.Kind != ResponderKindModel {
		t.Fatalf("expected model responder, got %q", responder.Kind)
	}
	if responder.ModelID != "openai/gpt-5.2" {
		t.Fatalf("expected model id to be preserved, got %q", responder.ModelID)
	}
	if responder.ContextLimit != 400000 {
		t.Fatalf("expected context limit 400000, got %d", responder.ContextLimit)
	}
	if !responder.SupportsVision {
		t.Fatal("expected vision support")
	}
}

func TestResolveResponderForAgentUsesAgentModelAndOverride(t *testing.T) {
	client := &AIClient{
		connector: &OpenAIConnector{},
		UserLogin: &bridgev2.UserLogin{UserLogin: &database.UserLogin{Metadata: &UserLoginMetadata{
			ModelCache: &ModelCache{Models: []ModelInfo{
				{ID: "openai/gpt-5.2", ContextWindow: 400000},
				{ID: "openai/gpt-4.1", ContextWindow: 128000},
			}},
			CustomAgents: map[string]*AgentDefinitionContent{
				"agent-1": {
					ID:    "agent-1",
					Name:  "Agent One",
					Model: "openai/gpt-5.2",
				},
			},
		}}},
	}

	responder, err := client.ResolveResponderForAgent(context.Background(), "agent-1", ResponderResolveOptions{})
	if err != nil {
		t.Fatalf("ResolveResponderForAgent returned error: %v", err)
	}
	if responder == nil {
		t.Fatal("expected responder")
	}
	if responder.Kind != ResponderKindAgent {
		t.Fatalf("expected agent responder, got %q", responder.Kind)
	}
	if responder.AgentID != "agent-1" {
		t.Fatalf("expected agent id agent-1, got %q", responder.AgentID)
	}
	if responder.ModelID != "openai/gpt-5.2" {
		t.Fatalf("expected agent primary model, got %q", responder.ModelID)
	}
	if responder.ContextLimit != 400000 {
		t.Fatalf("expected primary model context limit, got %d", responder.ContextLimit)
	}

	overridden, err := client.ResolveResponderForAgent(context.Background(), "agent-1", ResponderResolveOptions{
		RuntimeModelOverride: "openai/gpt-4.1",
	})
	if err != nil {
		t.Fatalf("ResolveResponderForAgent override returned error: %v", err)
	}
	if overridden.ModelID != "openai/gpt-4.1" {
		t.Fatalf("expected override model, got %q", overridden.ModelID)
	}
	if overridden.ContextLimit != 128000 {
		t.Fatalf("expected override context limit 128000, got %d", overridden.ContextLimit)
	}
	if overridden.GhostID != agentUserID("agent-1") {
		t.Fatalf("expected agent ghost identity to remain stable, got %q", overridden.GhostID)
	}
}

func TestResolveResponderForModelOverrideRecomputesGhostID(t *testing.T) {
	client := &AIClient{
		connector: &OpenAIConnector{},
		UserLogin: &bridgev2.UserLogin{UserLogin: &database.UserLogin{Metadata: &UserLoginMetadata{
			ModelCache: &ModelCache{Models: []ModelInfo{
				{ID: "openai/gpt-5.2", ContextWindow: 400000},
				{ID: "openai/gpt-4.1", ContextWindow: 128000},
			}},
		}}},
	}

	responder, err := client.resolveResponder(context.Background(), &PortalMetadata{
		ResolvedTarget: &ResolvedTarget{
			Kind:    ResolvedTargetModel,
			GhostID: modelUserID("openai/gpt-5.2"),
			ModelID: "openai/gpt-5.2",
		},
	}, ResponderResolveOptions{RuntimeModelOverride: "openai/gpt-4.1"})
	if err != nil {
		t.Fatalf("resolveResponder returned error: %v", err)
	}
	if responder.GhostID != modelUserID("openai/gpt-4.1") {
		t.Fatalf("expected override ghost id %q, got %q", modelUserID("openai/gpt-4.1"), responder.GhostID)
	}
}

func TestResponderFromModelInfoUnknownDoesNotAssumeToolCalling(t *testing.T) {
	responder := responderFromModelInfo(nil)
	if responder.SupportsToolCalling {
		t.Fatal("expected unknown model to not assume tool calling support")
	}
}
