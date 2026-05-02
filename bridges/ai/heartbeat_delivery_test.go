package ai

import (
	"context"
	"testing"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote/pkg/agents"
)

func cacheHeartbeatTestPortals(t *testing.T, client *AIClient, portals ...*bridgev2.Portal) {
	t.Helper()

	for _, portal := range portals {
		if portal == nil {
			continue
		}
		if client != nil && client.UserLogin != nil && client.UserLogin.Bridge != nil {
			persisted, err := client.UserLogin.Bridge.GetPortalByKey(context.Background(), portal.PortalKey)
			if err != nil {
				t.Fatalf("GetPortalByKey(%v) returned error: %v", portal.PortalKey, err)
			}
			persisted.Receiver = portal.Receiver
			persisted.OtherUserID = portal.OtherUserID
			persisted.MXID = portal.MXID
			persisted.Name = portal.Name
			persisted.Topic = portal.Topic
			persisted.Metadata = portal.Metadata
			if err = persisted.Save(context.Background()); err != nil {
				t.Fatalf("Save(%v) returned error: %v", portal.PortalKey, err)
			}
		}
	}
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

	session := "other-session"
	route, err := client.resolveHeartbeatRoute(agentID, &HeartbeatConfig{Session: &session})
	if err != nil {
		t.Fatalf("expected heartbeat route, got error: %v", err)
	}
	if route.Delivery.Portal == nil || route.Delivery.Portal.MXID != lastPortal.MXID {
		t.Fatalf("expected last active portal fallback to %q, got %#v", lastPortal.MXID, route.Delivery.Portal)
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
	session := otherPortal.MXID.String()

	route, err := client.resolveHeartbeatRoute(agentID, &HeartbeatConfig{Session: &session})
	if err != nil {
		t.Fatalf("expected fallback session portal, got error: %v", err)
	}
	if route.SessionPortal == nil || route.SessionPortal.MXID != lastPortal.MXID {
		t.Fatalf("expected last active portal fallback to %q, got %#v", lastPortal.MXID, route.SessionPortal)
	}
	if route.SessionPortal.MXID != lastPortal.MXID {
		t.Fatalf("expected last active room %q, got %q", lastPortal.MXID, route.SessionPortal.MXID)
	}
}

func TestResolveHeartbeatDeliveryTargetReturnsNoTargetWithoutHistory(t *testing.T) {
	client := newDBBackedTestAIClient(t, "")
	client.SetLoggedIn(true)

	agentID := normalizeAgentID(agents.DefaultAgentID)
	idlePortal := testAgentPortal("default", "!default:example.com", agentID, &PortalMetadata{
		ResolvedTarget: &ResolvedTarget{AgentID: agentID},
	})
	idlePortal.Receiver = client.UserLogin.ID
	cacheHeartbeatTestPortals(t, client, idlePortal)

	route, err := client.resolveHeartbeatRoute(agentID, nil)
	if err == nil {
		t.Fatalf("expected no session error, got route %#v", route)
	}
	if route.Delivery.Portal != nil {
		t.Fatalf("expected no delivery portal, got %#v", route.Delivery.Portal)
	}
}
