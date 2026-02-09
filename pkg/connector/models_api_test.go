package connector

import "testing"

func TestResolveAlias_StripsAnthropicDateSuffix(t *testing.T) {
	got := ResolveAlias("anthropic/claude-opus-4-20250514")
	if got != "anthropic/claude-opus-4.6" {
		t.Fatalf("unexpected alias resolution: got %q", got)
	}
}

func TestResolveAlias_MapsAnthropicBaseIDs(t *testing.T) {
	got := ResolveAlias("anthropic/claude-opus-4")
	if got != "anthropic/claude-opus-4.6" {
		t.Fatalf("unexpected alias resolution: got %q", got)
	}
}

func TestResolveAlias_DoesNotStripNonAnthropic(t *testing.T) {
	in := "openai/gpt-4o-2024-08-06"
	got := ResolveAlias(in)
	if got != in {
		t.Fatalf("unexpected alias resolution: got %q", got)
	}
}
