package ai

import (
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"
)

func TestPortalRoomNamePrefersBridgeOwnedName(t *testing.T) {
	portal := &bridgev2.Portal{
		Portal: &database.Portal{
			PortalKey: networkid.PortalKey{ID: networkid.PortalID("chat-1")},
			MXID:      id.RoomID("!chat:example.com"),
			Name:      "Bridge Name",
			Metadata: &PortalMetadata{
				Slug: "sidecar-slug",
			},
		},
	}

	if got := portalRoomName(portal); got != "Bridge Name" {
		t.Fatalf("expected bridge-owned room name, got %q", got)
	}

	portal.Name = ""
	if got := portalRoomName(portal); got != "sidecar-slug" {
		t.Fatalf("expected slug fallback when bridge name is empty, got %q", got)
	}
}
