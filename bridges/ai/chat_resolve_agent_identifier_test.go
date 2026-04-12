package ai

import (
	"context"
	"slices"
	"testing"

	"github.com/beeper/agentremote/pkg/agents"
)

func TestResolveAgentIdentifierContinuesWhenResponderResolutionFails(t *testing.T) {
	oc := newCatalogTestClient(t)
	agent := &agents.AgentDefinition{
		ID:   "missing-agent",
		Name: "Missing Agent",
		Model: agents.ModelConfig{
			Primary: "openai/gpt-5",
		},
	}

	resp, err := oc.resolveAgentIdentifier(context.Background(), agent, "", false)
	if err != nil {
		t.Fatalf("resolveAgentIdentifier returned error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected response")
	}
	if resp.UserID != agentUserIDForLogin(oc.UserLogin.ID, agent.ID) {
		t.Fatalf("unexpected user id %q", resp.UserID)
	}
	if resp.UserInfo == nil {
		t.Fatal("expected fallback user info")
	}
	if resp.UserInfo.Name == nil || *resp.UserInfo.Name != "Missing Agent" {
		t.Fatalf("unexpected fallback name %#v", resp.UserInfo.Name)
	}
	if !slices.Equal(resp.UserInfo.Identifiers, agentContactIdentifiers(agent.ID)) {
		t.Fatalf("unexpected identifiers %#v", resp.UserInfo.Identifiers)
	}
	if resp.UserInfo.ExtraProfile != nil {
		t.Fatalf("expected no responder metadata when resolution fails, got %#v", resp.UserInfo.ExtraProfile)
	}
}
