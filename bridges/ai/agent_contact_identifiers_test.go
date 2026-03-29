package ai

import "testing"

func TestAgentContactIdentifiers(t *testing.T) {
	identifiers := agentContactIdentifiers("beeper")
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
