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

func TestApprovalFlow_MatchReactionOwnerOnly(t *testing.T) {
	flow := NewApprovalFlow(ApprovalFlowConfig[any]{})
	expires := time.Now().Add(time.Minute)

	flow.mu.Lock()
	flow.registerPromptLocked(ApprovalPromptRegistration{
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
	flow.mu.Unlock()

	ownerMatch := flow.matchReaction(id.EventID("$prompt"), id.UserID("@owner:example.com"), "✅", time.Now())
	if !ownerMatch.KnownPrompt || !ownerMatch.ShouldResolve {
		t.Fatalf("expected owner reaction to resolve, got %#v", ownerMatch)
	}
	if !ownerMatch.Decision.Approved {
		t.Fatalf("expected approved decision, got %#v", ownerMatch.Decision)
	}

	otherMatch := flow.matchReaction(id.EventID("$prompt"), id.UserID("@other:example.com"), "✅", time.Now())
	if !otherMatch.KnownPrompt || otherMatch.ShouldResolve {
		t.Fatalf("expected non-owner reaction to be rejected, got %#v", otherMatch)
	}
	if otherMatch.RejectReason != RejectReasonOwnerOnly {
		t.Fatalf("expected reject reason %s, got %q", RejectReasonOwnerOnly, otherMatch.RejectReason)
	}
}
