package codex

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
	if content.Protocol.ID != "ai-codex" {
		t.Fatalf("expected ai-codex protocol, got %q", content.Protocol.ID)
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

func TestGetNameUsesDefaultCommandPrefixBeforeStartup(t *testing.T) {
	conn := NewConnector()
	if got := conn.GetName().DefaultCommandPrefix; got != "!codex" {
		t.Fatalf("expected default command prefix !codex, got %q", got)
	}
}

func TestApplyRuntimeDefaultsSetsCodexClientInfo(t *testing.T) {
	conn := NewConnector()
	conn.applyRuntimeDefaults()

	if conn.Config.Codex == nil || conn.Config.Codex.ClientInfo == nil {
		t.Fatal("expected codex client info defaults to be initialized")
	}
	if got := conn.Config.Codex.ClientInfo.Name; got != defaultCodexClientInfoName {
		t.Fatalf("expected codex client info name %q, got %q", defaultCodexClientInfoName, got)
	}
	if got := conn.Config.Codex.ClientInfo.Title; got != defaultCodexClientInfoTitle {
		t.Fatalf("expected codex client info title %q, got %q", defaultCodexClientInfoTitle, got)
	}
	if got := conn.Config.Codex.ClientInfo.Version; got != "0.1.0" {
		t.Fatalf("expected codex client info version 0.1.0, got %q", got)
	}
}
