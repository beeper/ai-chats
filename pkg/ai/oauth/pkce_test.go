package oauth

import (
	"regexp"
	"testing"
)

func TestGeneratePKCE(t *testing.T) {
	pkce, err := GeneratePKCE()
	if err != nil {
		t.Fatalf("unexpected generate pkce error: %v", err)
	}
	if pkce.Verifier == "" || pkce.Challenge == "" {
		t.Fatalf("expected verifier and challenge to be non-empty")
	}
	base64url := regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	if !base64url.MatchString(pkce.Verifier) {
		t.Fatalf("verifier must be base64url: %s", pkce.Verifier)
	}
	if !base64url.MatchString(pkce.Challenge) {
		t.Fatalf("challenge must be base64url: %s", pkce.Challenge)
	}
}
