package ai

import (
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"
)

func testAgentPortal(portalID, roomID, agentID string, meta *PortalMetadata) *bridgev2.Portal {
	return &bridgev2.Portal{
		Portal: &database.Portal{
			PortalKey:   networkid.PortalKey{ID: networkid.PortalID(portalID)},
			MXID:        id.RoomID(roomID),
			OtherUserID: agentUserID(agentID),
			Metadata:    meta,
		},
	}
}

func TestResolveSchedulableHeartbeatAgents(t *testing.T) {
	candidates := []heartbeatAgent{
		{agentID: "beeper"},
		{agentID: "worker"},
	}
	portals := []*bridgev2.Portal{
		testAgentPortal("visible", "!visible:example.com", "beeper", &PortalMetadata{Title: "Visible"}),
		testAgentPortal("hidden", "!hidden:example.com", "worker", &PortalMetadata{
			ModuleMeta: map[string]any{"heartbeat": map[string]any{"is_internal_room": true}},
		}),
		testAgentPortal("subagent", "!subagent:example.com", "worker", &PortalMetadata{
			SubagentParentRoomID: "!parent:example.com",
		}),
	}

	got := resolveSchedulableHeartbeatAgents(candidates, true, func(agentID string) bool {
		return agentID != "worker"
	}, portals)
	if len(got) != 1 || got[0].agentID != "beeper" {
		t.Fatalf("expected only beeper to be schedulable, got %#v", got)
	}

	got = resolveSchedulableHeartbeatAgents(candidates, true, func(string) bool { return true }, portals)
	if len(got) != 2 {
		t.Fatalf("expected both agents to be schedulable when they exist, got %#v", got)
	}

	got = resolveSchedulableHeartbeatAgents(candidates, false, func(string) bool { return true }, portals)
	if len(got) != 0 {
		t.Fatalf("expected no schedulable agents when agents are disabled, got %#v", got)
	}
}

func TestHeartbeatRoomsToCleanup(t *testing.T) {
	activeHeartbeat := testAgentPortal("heartbeat-active", "!active:example.com", "beeper", &PortalMetadata{
		ModuleMeta: map[string]any{"heartbeat": map[string]any{"is_internal_room": true}},
	})
	orphanHeartbeat := testAgentPortal("heartbeat-orphan", "!orphan:example.com", "worker", &PortalMetadata{
		ModuleMeta: map[string]any{"heartbeat": map[string]any{"is_internal_room": true}},
	})
	visible := testAgentPortal("visible", "!visible:example.com", "beeper", &PortalMetadata{Title: "Visible"})

	got := heartbeatRoomsToCleanup(
		[]*bridgev2.Portal{activeHeartbeat, orphanHeartbeat, visible},
		map[string]struct{}{"beeper": {}},
	)
	if len(got) != 1 || got[0] != orphanHeartbeat {
		t.Fatalf("expected only orphan heartbeat room to be cleaned up, got %#v", got)
	}
}
