package ai

import (
	"context"
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/pkg/agents"
)

func TestRecordAgentActivityOnlyWritesRoomSession(t *testing.T) {
	client := newDBBackedTestAIClient(t, "")
	agentID := normalizeAgentID(agents.DefaultAgentID)
	storeAgentID := client.resolveSessionRouting(agentID).StoreAgentID
	mainKey := client.resolveSessionRouting(agentID).MainKey

	portal := &bridgev2.Portal{
		Portal: &database.Portal{
			MXID: id.RoomID("!chat:example.com"),
		},
	}
	meta := &PortalMetadata{
		ResolvedTarget: &ResolvedTarget{AgentID: agentID},
	}

	client.recordAgentActivity(context.Background(), portal, meta)

	updatedAt, ok := client.loadStoredSessionUpdatedAt(context.Background(), storeAgentID, portal.MXID.String())
	if !ok {
		t.Fatalf("expected room session entry to be written")
	}
	if updatedAt <= 0 {
		t.Fatalf("expected room session entry to have an updated timestamp")
	}
	if _, ok := client.loadStoredSessionUpdatedAt(context.Background(), storeAgentID, mainKey); ok {
		t.Fatalf("expected main session row not to be created for route mirroring")
	}
}

func TestLoadLastRoutedSessionKeyIgnoresMainSessionRow(t *testing.T) {
	client := newDBBackedTestAIClient(t, "")
	agentID := normalizeAgentID(agents.DefaultAgentID)
	storeAgentID := client.resolveSessionRouting(agentID).StoreAgentID
	mainKey := client.resolveSessionRouting(agentID).MainKey

	if err := client.storeSessionUpdatedAt(context.Background(), storeAgentID, mainKey, 3_000); err != nil {
		t.Fatalf("upsert main session entry: %v", err)
	}
	if err := client.storeSessionUpdatedAt(context.Background(), storeAgentID, "!chat:example.com", 2_000); err != nil {
		t.Fatalf("upsert room session entry: %v", err)
	}

	target, ok := client.loadLastRoutedSessionKey(context.Background(), agentID)
	if !ok {
		t.Fatalf("expected last route to resolve")
	}
	if target != "!chat:example.com" {
		t.Fatalf("expected last route to ignore main session row, got target=%q", target)
	}
}

func TestResolveHeartbeatRouteDefaultDoesNotLoadMainSessionRoute(t *testing.T) {
	client := newDBBackedTestAIClient(t, "")
	agentID := normalizeAgentID(agents.DefaultAgentID)
	storeAgentID := client.resolveSessionRouting(agentID).StoreAgentID
	mainKey := client.resolveSessionRouting(agentID).MainKey

	if err := client.storeSessionUpdatedAt(context.Background(), storeAgentID, mainKey, 1_000); err != nil {
		t.Fatalf("upsert main session entry: %v", err)
	}
	defaultPortal := testAgentPortal("default", "!default:example.com", agentID, &PortalMetadata{
		ResolvedTarget: &ResolvedTarget{AgentID: agentID},
	})
	cacheHeartbeatTestPortals(t, client, defaultPortal)
	setUnexportedField(client.UserLogin.Bridge, "portalsByKey", map[networkid.PortalKey]*bridgev2.Portal{
		defaultChatPortalKey(client.UserLogin.ID): defaultPortal,
	})

	route, err := client.resolveHeartbeatRoute(agentID, nil)
	if err != nil {
		t.Fatalf("expected heartbeat route, got error: %v", err)
	}
	if route.Session.SessionKey != mainKey {
		t.Fatalf("expected main session key %q, got %q", mainKey, route.Session.SessionKey)
	}
	if route.Session.UpdatedAt != 0 {
		t.Fatalf("expected default heartbeat session resolution not to carry main session timestamp")
	}
}

func TestRecordAgentActivitySkipsInternalRooms(t *testing.T) {
	client := newDBBackedTestAIClient(t, "")
	agentID := normalizeAgentID(agents.DefaultAgentID)
	storeAgentID := client.resolveSessionRouting(agentID).StoreAgentID

	portal := &bridgev2.Portal{
		Portal: &database.Portal{
			MXID: id.RoomID("!internal:example.com"),
		},
	}
	meta := &PortalMetadata{
		InternalRoomKind: "heartbeat",
		ResolvedTarget:   &ResolvedTarget{AgentID: agentID},
	}

	client.recordAgentActivity(context.Background(), portal, meta)

	if _, ok := client.loadStoredSessionUpdatedAt(context.Background(), storeAgentID, portal.MXID.String()); ok {
		t.Fatalf("expected internal rooms not to write route state")
	}
}

func TestLoadLastRoutedSessionKeyUsesGlobalSessionStoreForNonDefaultAgent(t *testing.T) {
	client := newDBBackedTestAIClient(t, "")
	client.connector.Config.Session = &SessionConfig{Scope: sessionScopeGlobal}
	agentID := normalizeAgentID("custom-agent")

	portal := &bridgev2.Portal{
		Portal: &database.Portal{
			MXID: id.RoomID("!chat:example.com"),
		},
	}
	meta := &PortalMetadata{
		ResolvedTarget: &ResolvedTarget{AgentID: agentID},
	}

	client.recordAgentActivity(context.Background(), portal, meta)

	target, ok := client.loadLastRoutedSessionKey(context.Background(), agentID)
	if !ok {
		t.Fatalf("expected last route to resolve from shared global session store")
	}
	if target != "!chat:example.com" {
		t.Fatalf("expected global last route lookup to return room session, got target=%q", target)
	}
	if got := client.resolveSessionRouting(agentID).StoreAgentID; got != sessionScopeGlobal {
		t.Fatalf("expected global session store owner %q, got %q", sessionScopeGlobal, got)
	}
}
