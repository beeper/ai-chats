package fetch

import "testing"

func TestNormalizeRequestLeavesMaxCharsUnsetByDefault(t *testing.T) {
	got := normalizeRequest(Request{URL: "https://example.com", ExtractMode: "markdown"})
	if got.MaxChars != 0 {
		t.Fatalf("expected maxChars to remain unset (0), got %d", got.MaxChars)
	}
}
