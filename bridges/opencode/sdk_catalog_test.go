package opencode

import (
	"context"
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
)

func TestOpenCodeAgentCatalogListsSortedAgents(t *testing.T) {
	login := &bridgev2.UserLogin{
		UserLogin: &database.UserLogin{
			Metadata: &UserLoginMetadata{
				Provider: ProviderOpenCode,
				OpenCodeInstances: map[string]*OpenCodeInstance{
					"b": {ID: "b"},
					"a": {ID: "a"},
				},
			},
		},
	}
	agents, err := openCodeAgentCatalog{}.ListAgents(context.Background(), login)
	if err != nil {
		t.Fatalf("ListAgents returned error: %v", err)
	}
	if len(agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(agents))
	}
	if agents[0].ModelKey != "opencode:a" || agents[1].ModelKey != "opencode:b" {
		t.Fatalf("expected sorted model keys, got %q then %q", agents[0].ModelKey, agents[1].ModelKey)
	}
}

func TestOpenCodeAgentCatalogResolvesIdentifiers(t *testing.T) {
	login := &bridgev2.UserLogin{
		UserLogin: &database.UserLogin{
			Metadata: &UserLoginMetadata{
				Provider: ProviderOpenCode,
				OpenCodeInstances: map[string]*OpenCodeInstance{
					"abc123": {ID: "abc123"},
				},
			},
		},
	}
	agent, err := openCodeAgentCatalog{}.ResolveAgent(context.Background(), login, "opencode:abc123")
	if err != nil {
		t.Fatalf("ResolveAgent returned error: %v", err)
	}
	if agent == nil || agent.ID != string(OpenCodeUserID("abc123")) {
		t.Fatalf("unexpected agent: %#v", agent)
	}
}

func TestPortalMetadataCarriesSDKMetadata(t *testing.T) {
	meta := &PortalMetadata{}
	sdkMeta := meta.GetSDKPortalMetadata()
	if sdkMeta == nil {
		t.Fatal("expected SDK metadata carrier")
	}
	sdkMeta.Conversation.ArchiveOnCompletion = true
	meta.SetSDKPortalMetadata(sdkMeta)
	if !meta.SDK.Conversation.ArchiveOnCompletion {
		t.Fatal("expected SDK metadata to persist on portal metadata")
	}
}
