package beeperauth

import (
	"strings"
	"testing"
)

func TestNormalizeEmail(t *testing.T) {
	t.Parallel()

	got := normalizeEmail("  batuhan@example.com \n")
	if got != "batuhan@example.com" {
		t.Fatalf("unexpected normalized email: %q", got)
	}
}

func TestNormalizeLoginCode(t *testing.T) {
	t.Parallel()

	got := normalizeLoginCode(" 749  709\t")
	if got != "749709" {
		t.Fatalf("unexpected normalized code: %q", got)
	}
}

func TestLoginCodeResponseTokenPrefersLegacyFallback(t *testing.T) {
	t.Parallel()

	resp := &loginCodeResponse{LegacyLoginToken: " legacy-token "}
	if got := resp.token(); got != "legacy-token" {
		t.Fatalf("unexpected token: %q", got)
	}
}

func TestLoginCodeResponseSignupError(t *testing.T) {
	t.Parallel()

	resp := &loginCodeResponse{
		LeadToken:           "lead_123",
		UsernameSuggestions: []string{"alice", "alice2026"},
	}
	err := resp.signupError()
	if err == nil {
		t.Fatal("expected signup error")
	}
	if !strings.Contains(err.Error(), "finish registration in a Beeper client first") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), "alice, alice2026") {
		t.Fatalf("missing username suggestions in error: %v", err)
	}
}
