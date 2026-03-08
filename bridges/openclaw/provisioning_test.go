package openclaw

import "testing"

func TestOpenClawDMAgentSessionKey(t *testing.T) {
	got := openClawDMAgentSessionKey("Main")
	if got != "agent:main:matrix-dm" {
		t.Fatalf("unexpected synthetic dm session key: %q", got)
	}
	if !isOpenClawSyntheticDMSessionKey(got) {
		t.Fatalf("expected %q to be recognized as a synthetic dm session key", got)
	}
	if agentID := openClawAgentIDFromSessionKey(got); agentID != "main" {
		t.Fatalf("expected session key to resolve to canonical agent id, got %q", agentID)
	}
}

func TestParseOpenClawResolvableIdentifier(t *testing.T) {
	cases := map[string]string{
		"main":                "main",
		"openclaw:main":       "main",
		"openclaw-agent:main": "main",
	}
	for input, want := range cases {
		got, ok := parseOpenClawResolvableIdentifier(input)
		if !ok {
			t.Fatalf("expected %q to parse", input)
		}
		if got != want {
			t.Fatalf("unexpected parsed agent id for %q: got %q want %q", input, got, want)
		}
	}
	if _, ok := parseOpenClawResolvableIdentifier("   "); ok {
		t.Fatal("expected blank identifier to fail parsing")
	}
}

func TestSortConfiguredAgentsDefaultAndSearch(t *testing.T) {
	agents := []gatewayAgentSummary{
		{ID: "ops", Name: "Ops"},
		{ID: "main", Name: "Main"},
		{ID: "alpha", Identity: &gatewayAgentIdentity{Name: "Alpha Bot"}},
	}
	sorted := sortConfiguredAgents(agents, "main", "")
	if len(sorted) != 3 {
		t.Fatalf("expected 3 contacts, got %d", len(sorted))
	}
	if sorted[0].ID != "main" {
		t.Fatalf("expected default agent first, got %q", sorted[0].ID)
	}

	search := sortConfiguredAgents(agents, "main", "al")
	if len(search) != 1 || search[0].ID != "alpha" {
		t.Fatalf("unexpected search results: %#v", search)
	}

	search = sortConfiguredAgents(agents, "main", "op")
	if len(search) != 1 || search[0].ID != "ops" {
		t.Fatalf("unexpected prefix search results: %#v", search)
	}
}

func TestNormalizeGatewayAgentIdentityPrefersAvatarURL(t *testing.T) {
	identity := normalizeGatewayAgentIdentity(&gatewayAgentIdentity{
		AgentID:   "main",
		AvatarURL: "data:image/png;base64,Zm9v",
	})
	if identity == nil {
		t.Fatal("expected normalized identity")
	}
	if identity.Avatar != "data:image/png;base64,Zm9v" {
		t.Fatalf("expected avatar to fall back to avatarUrl, got %q", identity.Avatar)
	}
}
