package openclaw

import (
	"context"
	"testing"

	"github.com/beeper/agentremote/pkg/shared/cachedvalue"
	"github.com/beeper/agentremote/pkg/shared/openclawconv"
)

func TestOpenClawDMAgentSessionKey(t *testing.T) {
	got := openClawDMAgentSessionKey("Main")
	if got != "agent:main:matrix-dm" {
		t.Fatalf("unexpected synthetic dm session key: %q", got)
	}
	if !isOpenClawSyntheticDMSessionKey(got) {
		t.Fatalf("expected %q to be recognized as a synthetic dm session key", got)
	}
	if agentID := openclawconv.AgentIDFromSessionKey(got); agentID != "main" {
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

func TestOpenClawVirtualAgentSummary(t *testing.T) {
	agent := openClawVirtualAgentSummary("Codex")
	if agent == nil {
		t.Fatal("expected virtual agent")
	}
	if agent.ID != "codex" {
		t.Fatalf("unexpected virtual agent id: %q", agent.ID)
	}
	if openClawVirtualAgentSummary("gateway") != nil {
		t.Fatal("expected gateway to be excluded from virtual agent summaries")
	}
}

func TestMergeDiscoveredSessionAgents(t *testing.T) {
	oc := &OpenClawClient{
		manager: &openClawManager{
			sessions: map[string]gatewaySessionRow{
				"agent:main:main":            {Key: "agent:main:main"},
				"agent:ops:discord:dm:123":   {Key: "agent:ops:discord:dm:123"},
				"agent:alpha:subagent:child": {Key: "agent:alpha:subagent:child"},
			},
		},
	}
	merged := oc.mergeDiscoveredSessionAgents([]gatewayAgentSummary{
		{ID: "main", Name: "Main"},
	})
	if len(merged) != 3 {
		t.Fatalf("expected 3 merged agents, got %d", len(merged))
	}
	if merged[0].ID != "main" {
		t.Fatalf("expected existing agent to remain first, got %q", merged[0].ID)
	}
	found := map[string]bool{}
	for _, agent := range merged {
		found[agent.ID] = true
	}
	for _, want := range []string{"main", "ops", "alpha"} {
		if !found[want] {
			t.Fatalf("expected merged agents to include %q: %#v", want, merged)
		}
	}
}

func TestLoadAgentCatalogFallsBackToDiscoveredSessionAgents(t *testing.T) {
	oc := &OpenClawClient{
		manager: &openClawManager{
			sessions: map[string]gatewaySessionRow{
				"agent:main:matrix-dm": {Key: "agent:main:matrix-dm"},
			},
		},
	}

	agents, err := oc.loadAgentCatalog(context.Background(), false)
	if err != nil {
		t.Fatalf("expected discovered-session fallback, got error: %v", err)
	}
	if len(agents) != 1 || agents[0].ID != "main" {
		t.Fatalf("unexpected fallback agents: %#v", agents)
	}
}

func TestLoadAgentCatalogMergesDiscoveredSessionAgentsIntoFreshCache(t *testing.T) {
	agentCache := cachedvalue.New[agentCatalogEntry](openClawAgentCatalogTTL)
	agentCache.Update(agentCatalogEntry{
		Agents: []gatewayAgentSummary{
			{ID: "alpha", Name: "Alpha"},
		},
	})
	oc := &OpenClawClient{
		agentCache: agentCache,
		manager: &openClawManager{
			sessions: map[string]gatewaySessionRow{
				"agent:main:matrix-dm": {Key: "agent:main:matrix-dm"},
			},
		},
	}

	agents, err := oc.loadAgentCatalog(context.Background(), false)
	if err != nil {
		t.Fatalf("expected merged cached agents, got error: %v", err)
	}
	if len(agents) != 2 {
		t.Fatalf("expected cached and discovered agents, got %#v", agents)
	}
	found := map[string]bool{}
	for _, agent := range agents {
		found[agent.ID] = true
	}
	for _, want := range []string{"alpha", "main"} {
		if !found[want] {
			t.Fatalf("expected merged agent catalog to include %q: %#v", want, agents)
		}
	}
}

func TestOpenClawSpawnedSessionKeyFromToolResult(t *testing.T) {
	const childSessionKey = "agent:main:subagent:child"

	cases := []struct {
		name     string
		toolName string
		value    any
		want     string
	}{
		{
			name:     "map output",
			toolName: "sessions_spawn",
			value: map[string]any{
				"status":          "accepted",
				"childSessionKey": childSessionKey,
			},
			want: childSessionKey,
		},
		{
			name:     "nested json string output",
			toolName: "sessions_spawn",
			value:    `{"status":"accepted","result":{"childSessionKey":"agent:main:subagent:child"}}`,
			want:     childSessionKey,
		},
		{
			name:     "non spawn tool ignored",
			toolName: "bash",
			value: map[string]any{
				"childSessionKey": childSessionKey,
			},
			want: "",
		},
		{
			name:     "non child session ignored",
			toolName: "sessions_spawn",
			value: map[string]any{
				"childSessionKey": "agent:main:matrix-dm",
			},
			want: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := openClawSpawnedSessionKeyFromToolResult(tc.toolName, tc.value); got != tc.want {
				t.Fatalf("unexpected spawned session key: got %q want %q", got, tc.want)
			}
		})
	}
}
