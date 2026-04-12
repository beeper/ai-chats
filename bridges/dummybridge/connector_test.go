package dummybridge

import (
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"
)

func TestFillPortalBridgeInfoSetsAIRoomType(t *testing.T) {
	conn := NewConnector()
	portal := &bridgev2.Portal{Portal: &database.Portal{RoomType: database.RoomTypeDM}}
	content := &event.BridgeEventContent{}

	conn.FillPortalBridgeInfo(portal, content)
	if content.BeeperRoomTypeV2 != "dm" {
		t.Fatalf("expected dm room type, got %q", content.BeeperRoomTypeV2)
	}
	if content.Protocol.ID != "ai-dummybridge" {
		t.Fatalf("expected ai-dummybridge protocol, got %q", content.Protocol.ID)
	}
}

func TestGetCapabilitiesDoNotExposeSyntheticProvisioning(t *testing.T) {
	conn := NewConnector()
	caps := conn.GetCapabilities()
	if caps == nil {
		t.Fatal("expected capabilities")
	}
	if caps.Provisioning.ResolveIdentifier.CreateDM || caps.Provisioning.ResolveIdentifier.ContactList || caps.Provisioning.ResolveIdentifier.Search {
		t.Fatal("expected synthetic provisioning to be disabled")
	}
}

func TestGetNameUsesDefaultCommandPrefixBeforeStartup(t *testing.T) {
	conn := NewConnector()
	if got := conn.GetName().DefaultCommandPrefix; got != "!dummybridge" {
		t.Fatalf("expected default command prefix !dummybridge, got %q", got)
	}
}
