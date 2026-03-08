package connector

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
	if identifiers[0] != "beeper" {
		t.Fatalf("expected agent id first, got %q", identifiers[0])
	}
	foundModel := false
	for _, ident := range identifiers {
		if ident == modelID {
			foundModel = true
			break
		}
	}
	if !foundModel {
		t.Fatalf("expected model id %q to be present in identifiers: %#v", modelID, identifiers)
	}
}
