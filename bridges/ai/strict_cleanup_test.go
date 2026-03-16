package ai

import "testing"

func TestNormalizeModelAPIAcceptsOnlyCanonicalNames(t *testing.T) {
	if got := normalizeModelAPI("responses"); got != ModelAPIResponses {
		t.Fatalf("expected canonical responses API name, got %q", got)
	}
	if got := normalizeModelAPI("openai-responses"); got != "" {
		t.Fatalf("expected legacy alias to be rejected, got %q", got)
	}
}
