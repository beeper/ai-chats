package ai

import (
	"slices"
	"testing"
)

func TestModelContactIdentifiersUseStableIdentifiersOnly(t *testing.T) {
	modelID := "anthropic/claude-sonnet-4.6"
	info := &ModelInfo{
		ID:       modelID,
		Name:     "Claude Sonnet 4.6",
		Provider: "anthropic",
	}

	identifiers := modelContactIdentifiers(modelID, info)
	if len(identifiers) == 0 {
		t.Fatal("expected non-empty identifiers")
	}
	if !slices.Contains(identifiers, modelID) {
		t.Fatalf("expected canonical model id %q in identifiers: %#v", modelID, identifiers)
	}
	if slices.Contains(identifiers, info.Name) {
		t.Fatalf("did not expect display name %q in identifiers: %#v", info.Name, identifiers)
	}
	if slices.Contains(identifiers, info.Provider+"/"+info.Name) {
		t.Fatalf("did not expect provider/name alias in identifiers: %#v", identifiers)
	}
	for _, ident := range identifiers {
		if len(ident) >= 4 && ident[:4] == "uri:" {
			t.Fatalf("did not expect URI identifier in identifiers: %#v", identifiers)
		}
	}
}
