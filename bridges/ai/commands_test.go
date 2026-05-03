package ai

import "testing"

func TestThinkingNormalization(t *testing.T) {
	for _, value := range []string{"none", "low", "medium", "high"} {
		if got := normalizeThinking(value); got != value {
			t.Fatalf("normalizeThinking(%q) = %q", value, got)
		}
	}
	if got := normalizeThinking("everything"); got != "" {
		t.Fatalf("expected invalid thinking level to normalize empty, got %q", got)
	}
	if got := effectiveThinking(&PortalMetadata{}); got != "none" {
		t.Fatalf("expected default thinking none, got %q", got)
	}
}
