package ai

import (
	"context"
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/id"
)

func TestResolveMatrixSessionTarget_UsesMainPortal(t *testing.T) {
	ctx := context.Background()
	client := &AIClient{}
	currentPortal := &bridgev2.Portal{Portal: &database.Portal{MXID: id.RoomID("!main:example.com")}}

	target, err := client.resolveMatrixSessionTarget(ctx, currentPortal, " main ")
	if err != nil {
		t.Fatalf("resolve main target: %v", err)
	}
	if target.portal != currentPortal {
		t.Fatalf("expected current portal, got %#v", target.portal)
	}
	if target.displayKey != "main" {
		t.Fatalf("expected main display key, got %q", target.displayKey)
	}
}

func TestResolveMatrixSessionTarget_ResolvesRoomAndPortalIDs(t *testing.T) {
	ctx := context.Background()
	client := newDBBackedTestAIClient(t, ProviderOpenAI)
	portal := newTranscriptTestPortal(t, client, "session-target")

	byRoomID, err := client.resolveMatrixSessionTarget(ctx, nil, portal.MXID.String())
	if err != nil {
		t.Fatalf("resolve room target: %v", err)
	}
	if byRoomID.portal != portal {
		t.Fatalf("expected room lookup to return inserted portal, got %#v", byRoomID.portal)
	}
	if byRoomID.displayKey != portal.MXID.String() {
		t.Fatalf("expected room display key %q, got %q", portal.MXID, byRoomID.displayKey)
	}

	byPortalID, err := client.resolveMatrixSessionTarget(ctx, nil, string(portal.PortalKey.ID))
	if err != nil {
		t.Fatalf("resolve portal key target: %v", err)
	}
	if byPortalID.portal != portal {
		t.Fatalf("expected portal key lookup to return inserted portal, got %#v", byPortalID.portal)
	}
	if byPortalID.displayKey != portal.MXID.String() {
		t.Fatalf("expected portal key display key %q, got %q", portal.MXID, byPortalID.displayKey)
	}
}

func TestResolveMatrixSessionTarget_ReportsMissingAndUnavailableMain(t *testing.T) {
	ctx := context.Background()
	client := &AIClient{}

	if _, err := client.resolveMatrixSessionTarget(ctx, nil, "main"); err == nil || err.Error() != "main session not available" {
		t.Fatalf("expected unavailable main error, got %v", err)
	}
	if _, err := client.resolveMatrixSessionTarget(ctx, nil, "missing-session"); err == nil || err.Error() != "session not found: missing-session (use the sessionKey from sessions_list)" {
		t.Fatalf("expected missing session error, got %v", err)
	}
}
