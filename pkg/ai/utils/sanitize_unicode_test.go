package utils

import "testing"

func TestSanitizeSurrogates(t *testing.T) {
	in := "Hello 🙈 World"
	if got := SanitizeSurrogates(in); got != in {
		t.Fatalf("expected valid emoji pair unchanged, got %q", got)
	}

	invalidSurrogateBytes := string([]byte{0xED, 0xA0, 0xBD}) // UTF-8 bytes for surrogate range (invalid)
	got := SanitizeSurrogates("Text " + invalidSurrogateBytes + " here")
	if got != "Text  here" {
		t.Fatalf("expected invalid surrogate bytes removed, got %q", got)
	}
}
