package ai

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
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

func TestSearchUsersAndContactsHideAgentsWhenDisabled(t *testing.T) {
	enabled := false
	oc := &AIClient{
		UserLogin: &bridgev2.UserLogin{
			UserLogin: &database.UserLogin{
				ID: "login-1",
				Metadata: &UserLoginMetadata{
					Agents: &enabled,
					ModelCache: &ModelCache{
						Models: []ModelInfo{{
							ID:   "openai/gpt-5",
							Name: "GPT-5",
						}},
						LastRefresh:   time.Now().Unix(),
						CacheDuration: 3600,
					},
					CustomAgents: map[string]*AgentDefinitionContent{
						"custom-agent": {
							ID:    "custom-agent",
							Name:  "Custom Agent",
							Model: "openai/gpt-5",
						},
					},
				},
			},
		},
		connector: &OpenAIConnector{},
	}
	oc.SetLoggedIn(true)

	searchResults, err := oc.SearchUsers(context.Background(), "custom")
	if err != nil {
		t.Fatalf("SearchUsers returned error: %v", err)
	}
	if len(searchResults) != 0 {
		t.Fatalf("expected agent search results to be hidden, got %#v", searchResults)
	}

	searchResults, err = oc.SearchUsers(context.Background(), "gpt")
	if err != nil {
		t.Fatalf("SearchUsers returned error: %v", err)
	}
	if len(searchResults) != 1 || searchResults[0].UserID != modelUserID("openai/gpt-5") {
		t.Fatalf("expected only model search result, got %#v", searchResults)
	}

	contacts, err := oc.GetContactList(context.Background())
	if err != nil {
		t.Fatalf("GetContactList returned error: %v", err)
	}
	if len(contacts) != 1 || contacts[0].UserID != modelUserID("openai/gpt-5") {
		t.Fatalf("expected only model contact when agents are disabled, got %#v", contacts)
	}
}

func TestCreateChatWithGhostRejectsAgentWhenDisabled(t *testing.T) {
	enabled := false
	oc := &AIClient{
		UserLogin: &bridgev2.UserLogin{
			UserLogin: &database.UserLogin{
				ID: "login-1",
				Metadata: &UserLoginMetadata{
					Agents: &enabled,
				},
			},
		},
	}

	_, err := oc.CreateChatWithGhost(context.Background(), &bridgev2.Ghost{
		Ghost: &database.Ghost{
			ID: agentUserID("beeper"),
		},
	})
	if err == nil {
		t.Fatalf("expected agent ghost chat creation to be rejected")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "disabled") {
		t.Fatalf("expected disabled error, got %v", err)
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
