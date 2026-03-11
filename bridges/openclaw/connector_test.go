package openclaw

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
	meta.IsOpenClawRoom = true

	content := &event.BridgeEventContent{}
	conn.FillPortalBridgeInfo(portal, content)
	if content.BeeperRoomTypeV2 != "dm" {
		t.Fatalf("expected dm room type, got %q", content.BeeperRoomTypeV2)
	}
	if content.Protocol.ID != "ai-openclaw" {
		t.Fatalf("expected ai-openclaw protocol, got %q", content.Protocol.ID)
	}

	meta.IsOpenClawRoom = false
	portal.RoomType = database.RoomTypeDefault
	conn.FillPortalBridgeInfo(portal, content)
	if content.BeeperRoomTypeV2 != "group" {
		t.Fatalf("expected group room type for non-openclaw room, got %q", content.BeeperRoomTypeV2)
	}
}

func TestGetCapabilitiesDisablesDisappearingMessages(t *testing.T) {
	conn := NewConnector()
	caps := conn.GetCapabilities()
	if caps.DisappearingMessages {
		t.Fatal("expected disappearing messages to be disabled")
	}
	if !caps.Provisioning.ResolveIdentifier.CreateDM {
		t.Fatal("expected create DM provisioning to remain enabled")
	}
	if !caps.Provisioning.ResolveIdentifier.ContactList {
		t.Fatal("expected contact list provisioning to remain enabled")
	}
	if !caps.Provisioning.ResolveIdentifier.Search {
		t.Fatal("expected search provisioning to remain enabled")
	}
}
