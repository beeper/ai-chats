package ai

import "testing"

func TestGravatarProfileURLFromInput_Email(t *testing.T) {
	got, ok := gravatarProfileURLFromInput("Person@example.com")
	if !ok {
		t.Fatal("expected email input to resolve to a gravatar profile URL")
	}
	wantPrefix := gravatarAPIBaseURL + "/profiles/"
	if len(got) <= len(wantPrefix) || got[:len(wantPrefix)] != wantPrefix {
		t.Fatalf("expected gravatar profile URL, got %q", got)
	}
}

func TestGravatarProfileURLFromInput_URLPassthrough(t *testing.T) {
	if _, ok := gravatarProfileURLFromInput("https://example.com"); ok {
		t.Fatal("expected existing URL to not be rewritten")
	}
}
