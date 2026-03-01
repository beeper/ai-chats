package streamtransport

import (
	"testing"
)

func TestSplitAtMarkdownBoundary(t *testing.T) {
	text := "a\n\nb\n\nc"
	first, rest := SplitAtMarkdownBoundary(text, 4)
	if first == "" || rest == "" {
		t.Fatalf("expected split result, got first=%q rest=%q", first, rest)
	}
}
