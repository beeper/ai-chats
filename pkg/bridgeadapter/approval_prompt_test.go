package bridgeadapter

import (
	"testing"
	"time"

	"maunium.net/go/mautrix/id"
)

func TestBuildApprovalPromptMessage_UsesApprovalDecisionMetadata(t *testing.T) {
	msg := BuildApprovalPromptMessage(ApprovalPromptMessageParams{
		ApprovalID: "approval-1",
		ToolCallID: "tool-1",
		ToolName:   "message",
		TurnID:     "turn-1",
		ExpiresAt:  time.UnixMilli(12345),
	})
	raw := msg.Raw
	approvalRaw, ok := raw[ApprovalDecisionKey].(map[string]any)
	if !ok {
		t.Fatalf("expected %s metadata map", ApprovalDecisionKey)
	}
	if approvalRaw["kind"] != "request" {
		t.Fatalf("expected kind=request, got %#v", approvalRaw["kind"])
	}
	if approvalRaw["approvalId"] != "approval-1" {
		t.Fatalf("expected approvalId=approval-1, got %#v", approvalRaw["approvalId"])
	}
}

func TestApprovalPromptStore_MatchReactionOwnerOnly(t *testing.T) {
	store := NewApprovalPromptStore()
	expires := time.Now().Add(time.Minute)
	store.Register(ApprovalPromptRegistration{
		ApprovalID:    "approval-1",
		RoomID:        id.RoomID("!room:example.com"),
		OwnerMXID:     id.UserID("@owner:example.com"),
		ToolCallID:    "tool-1",
		PromptEventID: id.EventID("$prompt"),
		ExpiresAt:     expires,
		Options: []ApprovalOption{
			{ID: "allow_once", Key: "✅", Approved: true},
		},
	})

	ownerMatch := store.MatchReaction(id.EventID("$prompt"), id.UserID("@owner:example.com"), "✅", time.Now())
	if !ownerMatch.KnownPrompt || !ownerMatch.ShouldResolve {
		t.Fatalf("expected owner reaction to resolve, got %#v", ownerMatch)
	}
	if !ownerMatch.Decision.Approved {
		t.Fatalf("expected approved decision, got %#v", ownerMatch.Decision)
	}

	otherMatch := store.MatchReaction(id.EventID("$prompt"), id.UserID("@other:example.com"), "✅", time.Now())
	if !otherMatch.KnownPrompt || otherMatch.ShouldResolve {
		t.Fatalf("expected non-owner reaction to be rejected, got %#v", otherMatch)
	}
	if otherMatch.RejectReason != "only_owner" {
		t.Fatalf("expected reject reason only_owner, got %q", otherMatch.RejectReason)
	}
}
