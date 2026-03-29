package ai

import "testing"

func TestAgentContactIdentifiers(t *testing.T) {
	modelID := "openrouter/openai/gpt-4.1"
	info := &ModelInfo{
		ID:       modelID,
		Name:     "GPT-4.1",
		Provider: "openrouter",
	}
	identifiers := agentContactIdentifiers("beeper", modelID, info)
	if len(identifiers) == 0 {
		t.Fatalf("expected non-empty identifiers")
	}
	if identifiers[0] != "agent:beeper" {
		t.Fatalf("expected agent id first, got %q", identifiers[0])
	}
	if len(identifiers) != 1 {
		t.Fatalf("expected only canonical agent identifier, got %#v", identifiers)
	}
}
