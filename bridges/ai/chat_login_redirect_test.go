package ai

import (
	"context"
	"slices"
	"strings"
	"testing"
)

func TestSearchUsersRequiresLogin(t *testing.T) {
	oc := &AIClient{}
	_, err := oc.SearchUsers(context.Background(), "gpt")
	if err == nil {
		t.Fatalf("expected login error from SearchUsers")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "logged in") {
		t.Fatalf("expected logged-in message, got: %v", err)
	}
}

func TestGetContactListRequiresLogin(t *testing.T) {
	oc := &AIClient{}
	_, err := oc.GetContactList(context.Background())
	if err == nil {
		t.Fatalf("expected login error from GetContactList")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "logged in") {
		t.Fatalf("expected logged-in message, got: %v", err)
	}
}

func TestModelRedirectTarget(t *testing.T) {
	tests := []struct {
		name     string
		request  string
		resolved string
		wantSet  bool
	}{
		{name: "same", request: "openrouter/openai/gpt-4.1", resolved: "openrouter/openai/gpt-4.1", wantSet: false},
		{name: "different", request: "my-alias", resolved: "openrouter/openai/gpt-4.1", wantSet: true},
		{name: "empty request", request: "", resolved: "openrouter/openai/gpt-4.1", wantSet: false},
		{name: "empty resolved", request: "my-alias", resolved: "", wantSet: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := modelRedirectTarget(tc.request, tc.resolved)
			if tc.wantSet && got == "" {
				t.Fatalf("expected redirect target for request=%q resolved=%q", tc.request, tc.resolved)
			}
			if !tc.wantSet && got != "" {
				t.Fatalf("expected no redirect target, got %q", got)
			}
		})
	}
}

func TestResolveModelIDFromManifestAcceptsRawModelID(t *testing.T) {
	const modelID = "google/gemini-2.0-flash-lite-001"
	if got := resolveModelIDFromManifest(modelID); got != modelID {
		t.Fatalf("expected raw model ID %q to resolve, got %q", modelID, got)
	}
}

func TestResolveModelIDFromManifestAcceptsEncodedModelIDViaCandidates(t *testing.T) {
	const encoded = "google%2Fgemini-2.0-flash-lite-001"
	candidates := candidateModelLookupIDs(encoded)
	const canonical = "google/gemini-2.0-flash-lite-001"
	if !slices.Contains(candidates, canonical) {
		t.Fatalf("expected decoded model candidate in %#v", candidates)
	}
	for _, candidate := range candidates {
		if got := resolveModelIDFromManifest(candidate); got == canonical {
			return
		}
	}
	t.Fatalf("expected one of %#v to resolve to canonical model %q", candidates, canonical)
}

func TestCandidateModelLookupIDsRejectsMalformedEncoding(t *testing.T) {
	candidates := candidateModelLookupIDs("model-%ZZ")
	if len(candidates) != 1 || candidates[0] != "model-%ZZ" {
		t.Fatalf("expected malformed encoding to remain unchanged, got %#v", candidates)
	}
}

func TestParseModelFromGhostIDAcceptsEscapedGhostID(t *testing.T) {
	const ghostID = "model-google%2Fgemini-2.0-flash-lite-001"
	const want = "google/gemini-2.0-flash-lite-001"
	if got := parseModelFromGhostID(ghostID); got != want {
		t.Fatalf("expected ghost ID %q to parse to %q, got %q", ghostID, want, got)
	}
}

func TestParseModelFromGhostIDRejectsMalformedEscaping(t *testing.T) {
	if got := parseModelFromGhostID("model-%ZZ"); got != "" {
		t.Fatalf("expected malformed ghost ID to be rejected, got %q", got)
	}
}
