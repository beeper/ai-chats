package ai

import "testing"

func TestCanonicalPromptToolArgumentsJSONEncodesPlainStrings(t *testing.T) {
	if got := canonicalPromptToolArguments("hello"); got != `"hello"` {
		t.Fatalf("expected plain string to be JSON-encoded, got %q", got)
	}
}

func TestCanonicalPromptToolArgumentsPreservesJSONStrings(t *testing.T) {
	if got := canonicalPromptToolArguments(`{"query":"matrix"}`); got != `{"query":"matrix"}` {
		t.Fatalf("expected JSON string to stay canonical JSON, got %q", got)
	}
}
