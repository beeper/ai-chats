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

func TestAgentHasUserChat(t *testing.T) {
	portals := []*bridgev2.Portal{
		testAgentPortal("chat-1", "!chat1:example.com", "beeper", &PortalMetadata{Title: "Chat"}),
		testAgentPortal("heartbeat", "!hb:example.com", "beeper", &PortalMetadata{
			ModuleMeta: map[string]any{"heartbeat": map[string]any{"is_internal_room": true}},
		}),
		testAgentPortal("subagent", "!sub:example.com", "beeper", &PortalMetadata{
			SubagentParentRoomID: "!parent:example.com",
		}),
	}

	if !agentHasUserChat(portals, "beeper") {
		t.Fatal("expected beeper to have a user chat")
	}
	if agentHasUserChat(portals, "worker") {
		t.Fatal("expected worker to have no user chat")
	}
	// Internal and subagent rooms should not count.
	internalOnly := []*bridgev2.Portal{portals[1], portals[2]}
	if agentHasUserChat(internalOnly, "beeper") {
		t.Fatal("expected internal-only portals not to count as user chats")
	}
}
