package connector

import (
	"slices"
	"testing"
)

func TestModelContactIdentifiersUseStableIdentifiersOnly(t *testing.T) {
	modelID := "anthropic/claude-sonnet-4.6"

	identifiers := modelContactIdentifiers(modelID)
	if len(identifiers) == 0 {
		t.Fatal("expected non-empty identifiers")
	}
	if len(identifiers) != 1 {
		t.Fatalf("expected only canonical model identifier, got %#v", identifiers)
	}
	if !slices.Contains(identifiers, "model:"+modelID) {
		t.Fatalf("expected canonical model identifier %q in identifiers: %#v", "model:"+modelID, identifiers)
	}
}
