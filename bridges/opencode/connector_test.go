package opencode

import (
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"
)

func TestFillPortalBridgeInfoSetsAIRoomType(t *testing.T) {
	conn := NewConnector()
	portal := &bridgev2.Portal{Portal: &database.Portal{RoomType: database.RoomTypeDM}}
	meta := portalMeta(portal)
	meta.IsOpenCodeRoom = true

	content := &event.BridgeEventContent{}
	conn.FillPortalBridgeInfo(portal, content)
	if content.BeeperRoomTypeV2 != "dm" {
		t.Fatalf("expected dm room type, got %q", content.BeeperRoomTypeV2)
	}
	if content.Protocol.ID != "ai-opencode" {
		t.Fatalf("expected ai-opencode protocol, got %q", content.Protocol.ID)
	}

	meta.IsOpenCodeRoom = false
	portal.RoomType = database.RoomTypeDefault
	conn.FillPortalBridgeInfo(portal, content)
	if content.BeeperRoomTypeV2 != "group" {
		t.Fatalf("expected group room type for non-opencode room, got %q", content.BeeperRoomTypeV2)
	}
}

func TestGetCapabilitiesEnablesContactListProvisioning(t *testing.T) {
	conn := NewConnector()
	caps := conn.GetCapabilities()
	if caps == nil {
		t.Fatal("expected capabilities")
	}
	if !caps.Provisioning.ResolveIdentifier.ContactList {
		t.Fatal("expected contact list provisioning to be enabled")
	}
}
