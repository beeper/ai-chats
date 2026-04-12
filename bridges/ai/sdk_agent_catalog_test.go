package ai

import (
	"context"
	"slices"
	"testing"

	"github.com/beeper/agentremote/pkg/agents"
	"github.com/beeper/agentremote/sdk"
)

func newCatalogTestClient(t *testing.T) *AIClient {
	enabled := true
	client := newDBBackedTestAIClient(t, "")
	client.connector = &OpenAIConnector{}
	setTestLoginConfig(client, &aiLoginConfig{Agents: &enabled})
	setTestLoginState(client, &loginRuntimeState{
		ModelCache: &ModelCache{
			Models: []ModelInfo{{
				ID:                  "openai/gpt-5",
				Name:                "GPT-5",
				SupportsToolCalling: true,
			}},
		},
	})
	seedTestCustomAgent(t, client, &AgentDefinitionContent{
		ID:          "custom-agent",
		Name:        "Custom Agent",
		Description: "Handles custom workflows",
		AvatarURL:   "mxc://example.com/custom",
		Model:       "openai/gpt-5",
	})
	return client
}

func newCatalogTestClientAgentsDisabled(t *testing.T) *AIClient {
	client := newCatalogTestClient(t)
	enabled := false
	setTestLoginConfig(client, &aiLoginConfig{Agents: &enabled})
	return client
}

func TestAIAgentCatalogDefaultAgent(t *testing.T) {
	client := newCatalogTestClient(t)

	agent, err := client.sdkAgentCatalog().DefaultAgent(context.Background(), client.UserLogin)
	if err != nil {
		t.Fatalf("DefaultAgent returned error: %v", err)
	}
	if agent == nil {
		t.Fatal("expected default agent")
	}
	agentID, ok := parseAgentFromGhostID(agent.ID)
	if !ok || agentID != agents.DefaultAgentID {
		t.Fatalf("expected default agent id %q, got %#v", agents.DefaultAgentID, agent)
	}
}

func TestAIAgentCatalogListsAndResolvesCustomAgents(t *testing.T) {
	client := newCatalogTestClient(t)
	catalog := client.sdkAgentCatalog()

	agentsList, err := catalog.ListAgents(context.Background(), client.UserLogin)
	if err != nil {
		t.Fatalf("ListAgents returned error: %v", err)
	}
	var customAgent *sdk.Agent
	for _, agent := range agentsList {
		if agent != nil && agent.Name == "Custom Agent" {
			customAgent = agent
			break
		}
	}
	if customAgent == nil {
		t.Fatalf("expected custom agent in catalog, got %#v", agentsList)
	}
	if got := customAgent.ID; got != string(agentUserIDForLogin(client.UserLogin.ID, "custom-agent")) {
		t.Fatalf("unexpected custom agent ghost id %q", got)
	}

	resolved, err := catalog.ResolveAgent(context.Background(), client.UserLogin, "custom-agent")
	if err != nil {
		t.Fatalf("ResolveAgent returned error for bare id: %v", err)
	}
	if resolved == nil || resolved.ID != customAgent.ID {
		t.Fatalf("unexpected bare-id resolution result: %#v", resolved)
	}

	resolved, err = catalog.ResolveAgent(context.Background(), client.UserLogin, customAgent.ID)
	if err != nil {
		t.Fatalf("ResolveAgent returned error for ghost id: %v", err)
	}
	if resolved == nil || resolved.ID != customAgent.ID {
		t.Fatalf("unexpected ghost-id resolution result: %#v", resolved)
	}
	if !slices.Contains(resolved.Identifiers, "agent:custom-agent") {
		t.Fatalf("expected canonical agent identifier in identifiers, got %#v", resolved.Identifiers)
	}
	if resolved.AvatarURL != "mxc://example.com/custom" {
		t.Fatalf("expected avatar URL to be preserved, got %q", resolved.AvatarURL)
	}

	resolved, err = catalog.ResolveAgent(context.Background(), client.UserLogin, "agent:custom-agent")
	if err != nil {
		t.Fatalf("ResolveAgent returned error for canonical identifier: %v", err)
	}
	if resolved == nil || resolved.ID != customAgent.ID {
		t.Fatalf("unexpected canonical-id resolution result: %#v", resolved)
	}
}

func TestAIAgentCatalogHidesAgentsWhenDisabled(t *testing.T) {
	client := newCatalogTestClientAgentsDisabled(t)
	catalog := client.sdkAgentCatalog()

	agent, err := catalog.DefaultAgent(context.Background(), client.UserLogin)
	if err != nil {
		t.Fatalf("DefaultAgent returned error: %v", err)
	}
	if agent != nil {
		t.Fatalf("expected no default agent when agents are disabled, got %#v", agent)
	}

	agentsList, err := catalog.ListAgents(context.Background(), client.UserLogin)
	if err != nil {
		t.Fatalf("ListAgents returned error: %v", err)
	}
	if len(agentsList) != 0 {
		t.Fatalf("expected no listed agents when agents are disabled, got %#v", agentsList)
	}

	resolved, err := catalog.ResolveAgent(context.Background(), client.UserLogin, "custom-agent")
	if err != nil {
		t.Fatalf("ResolveAgent returned error: %v", err)
	}
	if resolved != nil {
		t.Fatalf("expected agent resolution to be disabled, got %#v", resolved)
	}
}
