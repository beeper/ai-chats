package ai

import (
	"context"
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/pkg/agents"
)

func cacheHeartbeatTestPortals(t *testing.T, client *AIClient, portals ...*bridgev2.Portal) {
	t.Helper()

	byKey := make(map[networkid.PortalKey]*bridgev2.Portal, len(portals))
	byMXID := make(map[id.RoomID]*bridgev2.Portal, len(portals))
	for _, portal := range portals {
		if portal == nil {
			continue
		}
		byKey[portal.PortalKey] = portal
		if portal.MXID != "" {
			byMXID[portal.MXID] = portal
		}
	}
	setUnexportedField(client.UserLogin.Bridge, "portalsByKey", byKey)
	setUnexportedField(client.UserLogin.Bridge, "portalsByMXID", byMXID)
}

func TestResolveHeartbeatDeliveryTargetFallsBackFromMismatchedSessionRoom(t *testing.T) {
	client := newDBBackedTestAIClient(t, "")
	client.SetLoggedIn(true)

	agentID := normalizeAgentID(agents.DefaultAgentID)
	lastPortal := testAgentPortal("last", "!last:example.com", agentID, &PortalMetadata{
		ResolvedTarget: &ResolvedTarget{AgentID: agentID},
	})
	otherPortal := testAgentPortal("other", "!other:example.com", "other-agent", &PortalMetadata{
		ResolvedTarget: &ResolvedTarget{AgentID: "other-agent"},
	})
	cacheHeartbeatTestPortals(t, client, lastPortal, otherPortal)

	client.recordAgentActivity(context.Background(), lastPortal, portalMeta(lastPortal))

	route, err := client.resolveHeartbeatRoute(agentID, nil, heartbeatSessionResolution{SessionKey: otherPortal.MXID.String()})
	if err != nil {
		t.Fatalf("expected heartbeat route, got error: %v", err)
	}
	if route.Delivery.Portal != lastPortal {
		t.Fatalf("expected last active portal fallback, got %#v", route.Delivery.Portal)
	}
	if route.Delivery.RoomID != lastPortal.MXID {
		t.Fatalf("expected last active room %q, got %q", lastPortal.MXID, route.Delivery.RoomID)
	}
	if route.Delivery.Reason != "last-active" {
		t.Fatalf("expected last-active reason, got %q", route.Delivery.Reason)
	}
}

func TestResolveHeartbeatRouteFallsBackFromMismatchedExplicitSessionRoom(t *testing.T) {
	client := newDBBackedTestAIClient(t, "")

	agentID := normalizeAgentID(agents.DefaultAgentID)
	lastPortal := testAgentPortal("last", "!last:example.com", agentID, &PortalMetadata{
		ResolvedTarget: &ResolvedTarget{AgentID: agentID},
	})
	otherPortal := testAgentPortal("other", "!other:example.com", "other-agent", &PortalMetadata{
		ResolvedTarget: &ResolvedTarget{AgentID: "other-agent"},
	})
	cacheHeartbeatTestPortals(t, client, lastPortal, otherPortal)

	client.recordAgentActivity(context.Background(), lastPortal, portalMeta(lastPortal))
	session := otherPortal.MXID.String()

	route, err := client.resolveHeartbeatRoute(agentID, &HeartbeatConfig{Session: &session})
	if err != nil {
		t.Fatalf("expected fallback session portal, got error: %v", err)
	}
	if route.SessionPortal != lastPortal {
		t.Fatalf("expected last active portal fallback, got %#v", route.SessionPortal)
	}
	if route.SessionKey != lastPortal.MXID.String() {
		t.Fatalf("expected last active room %q, got %q", lastPortal.MXID, route.SessionKey)
	}
}

func TestResolveHeartbeatDeliveryTargetFallsBackToDefaultChat(t *testing.T) {
	client := newDBBackedTestAIClient(t, "")
	client.SetLoggedIn(true)

	agentID := normalizeAgentID(agents.DefaultAgentID)
	defaultPortal := testAgentPortal("default", "!default:example.com", agentID, &PortalMetadata{
		ResolvedTarget: &ResolvedTarget{AgentID: agentID},
	})
	cacheHeartbeatTestPortals(t, client, defaultPortal)
	setUnexportedField(client.UserLogin.Bridge, "portalsByKey", map[networkid.PortalKey]*bridgev2.Portal{
		defaultChatPortalKey(client.UserLogin.ID): defaultPortal,
	})

	route, err := client.resolveHeartbeatRoute(agentID, nil, heartbeatSessionResolution{})
	if err != nil {
		t.Fatalf("expected heartbeat route, got error: %v", err)
	}
	if route.Delivery.Portal != defaultPortal {
		t.Fatalf("expected default chat portal fallback, got %#v", route.Delivery.Portal)
	}
	if route.Delivery.Reason != "default-chat" {
		t.Fatalf("expected default-chat reason, got %q", route.Delivery.Reason)
	}
}
