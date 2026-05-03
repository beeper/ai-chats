package connector

import (
	"strings"
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"
)

func TestBuildRoomIdentityHint_IncludesRoomID(t *testing.T) {
	portal := &bridgev2.Portal{Portal: &database.Portal{}}
	portal.MXID = id.RoomID("!room:example.org")
	portal.PortalKey = networkid.PortalKey{ID: networkid.PortalID("portal-123")}

	meta := modelModeTestMeta("openai/gpt-5")
	got := buildRoomIdentityHint(portal, meta)
	if got == "" {
		t.Fatalf("expected non-empty hint")
	}
	if !strings.Contains(got, "room_id: !room:example.org") {
		t.Fatalf("expected hint to include room id, got %q", got)
	}
}

func TestBuildRoomIdentityHint_InternalRoomIncludesRoomID(t *testing.T) {
	portal := &bridgev2.Portal{Portal: &database.Portal{}}
	portal.MXID = id.RoomID("!internal:example.org")
	meta := &PortalMetadata{InternalRoomKind: "codex"}
	got := buildRoomIdentityHint(portal, meta)
	if !strings.Contains(got, "room_id: !internal:example.org") {
		t.Fatalf("expected room id in hint, got %q", got)
	}
}
