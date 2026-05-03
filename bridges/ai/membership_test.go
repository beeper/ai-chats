package ai

import (
	"context"
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

func TestHandleMatrixMembershipSwitchesModelGhost(t *testing.T) {
	ctx := context.Background()
	oc := newDBBackedTestAIClient(t, ProviderOpenRouter)
	portal := testAIModelPortal(t, oc, "openai/gpt-5.4")
	portal.MXID = id.RoomID("!room:example.com")

	ghost, err := oc.UserLogin.Bridge.GetGhostByID(ctx, modelUserID("google/gemini-2.5-pro"))
	if err != nil {
		t.Fatalf("get ghost: %v", err)
	}
	result, err := oc.HandleMatrixMembership(ctx, &bridgev2.MatrixMembershipChange{
		MatrixRoomMeta: bridgev2.MatrixRoomMeta[*event.MemberEventContent]{
			MatrixEventBase: bridgev2.MatrixEventBase[*event.MemberEventContent]{
				Portal:  portal,
				Content: &event.MemberEventContent{Membership: event.MembershipInvite},
			},
		},
		Target: ghost,
		Type:   bridgev2.Invite,
	})
	if err != nil {
		t.Fatalf("handle membership: %v", err)
	}
	if result != nil {
		t.Fatalf("unexpected redirect: %#v", result)
	}
	if portal.OtherUserID != modelUserID("google/gemini-2.5-pro") {
		t.Fatalf("expected switched portal target, got %q", portal.OtherUserID)
	}
	meta := portalMeta(portal)
	if meta.ResolvedTarget == nil || meta.ResolvedTarget.ModelID != "google/gemini-2.5-pro" {
		t.Fatalf("expected resolved gemini target, got %#v", meta.ResolvedTarget)
	}
	caps := oc.GetCapabilities(ctx, portal)
	if caps.File[event.MsgAudio].MimeTypes["audio/mpeg"] != event.CapLevelFullySupported {
		t.Fatalf("expected audio upload support after model switch")
	}
	if caps.File[event.MsgVideo].MimeTypes["video/mp4"] != event.CapLevelFullySupported {
		t.Fatalf("expected video upload support after model switch")
	}
}

func TestHandleMatrixMembershipRejectsNonModelGhost(t *testing.T) {
	ctx := context.Background()
	oc := newDBBackedTestAIClient(t, ProviderOpenAI)
	portal := testAIModelPortal(t, oc, "openai/gpt-5.4")
	ghost, err := oc.UserLogin.Bridge.GetGhostByID(ctx, "not-a-model")
	if err != nil {
		t.Fatalf("get ghost: %v", err)
	}
	_, err = oc.HandleMatrixMembership(ctx, &bridgev2.MatrixMembershipChange{
		MatrixRoomMeta: bridgev2.MatrixRoomMeta[*event.MemberEventContent]{
			MatrixEventBase: bridgev2.MatrixEventBase[*event.MemberEventContent]{Portal: portal},
		},
		Target: ghost,
		Type:   bridgev2.Invite,
	})
	if err == nil {
		t.Fatalf("expected unsupported ghost error")
	}
	if portal.OtherUserID != modelUserID("openai/gpt-5.4") {
		t.Fatalf("rejected invite should not mutate portal target, got %q", portal.OtherUserID)
	}
}

func testAIModelPortal(t *testing.T, oc *AIClient, modelID string) *bridgev2.Portal {
	t.Helper()
	ctx := context.Background()
	portal, err := oc.UserLogin.Bridge.GetPortalByKey(ctx, networkid.PortalKey{
		ID:       networkid.PortalID("chat-" + modelID),
		Receiver: oc.UserLogin.ID,
	})
	if err != nil {
		t.Fatalf("get portal: %v", err)
	}
	portal.OtherUserID = modelUserID(modelID)
	portal.Metadata = &PortalMetadata{Slug: "test"}
	setPortalResolvedTarget(portal, portalMeta(portal), modelUserID(modelID))
	if err := oc.savePortal(ctx, portal, "test portal"); err != nil {
		t.Fatalf("save portal: %v", err)
	}
	return portal
}
