package connector

import (
	"context"
	"testing"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/ai-bridge/pkg/bridgeadapter"
	airuntime "github.com/beeper/ai-bridge/pkg/runtime"
)

func newTestAIClient(owner id.UserID) *AIClient {
	ul := &bridgev2.UserLogin{}
	ul.UserLogin = &database.UserLogin{
		UserMXID: owner,
		Metadata: &UserLoginMetadata{},
	}
	return &AIClient{
		UserLogin: ul,
		approvals: bridgeadapter.NewApprovalManager[toolApprovalResolution](),
	}
}

func TestToolApprovals_Resolve(t *testing.T) {
	owner := id.UserID("@owner:example.com")
	roomID := id.RoomID("!room:example.com")

	oc := newTestAIClient(owner)

	approvalID := "approval-1"
	oc.registerToolApproval(ToolApprovalParams{
		ApprovalID:   approvalID,
		RoomID:       roomID,
		TurnID:       "turn-1",
		ToolCallID:   "call-1",
		ToolName:     "message",
		ToolKind:     ToolApprovalKindBuiltin,
		RuleToolName: "message",
		Action:       "send",
		TTL:          2 * time.Second,
	})

	if err := oc.resolveToolApproval(
		roomID,
		approvalID,
		airuntime.ToolApprovalDecision{State: airuntime.ToolApprovalApproved},
		false,
		owner,
	); err != nil {
		t.Fatalf("resolve failed: %v", err)
	}

	resolution, _, ok := oc.waitToolApproval(context.Background(), approvalID)
	if !ok {
		t.Fatalf("expected wait ok")
	}
	if !approvalAllowed(resolution.Decision) {
		t.Fatalf("expected approve=true")
	}
}

func TestToolApprovals_RejectNonOwner(t *testing.T) {
	owner := id.UserID("@owner:example.com")
	roomID := id.RoomID("!room:example.com")

	oc := newTestAIClient(owner)
	approvalID := "approval-1"
	oc.registerToolApproval(ToolApprovalParams{
		ApprovalID:   approvalID,
		RoomID:       roomID,
		TurnID:       "turn-1",
		ToolCallID:   "call-1",
		ToolName:     "message",
		ToolKind:     ToolApprovalKindBuiltin,
		RuleToolName: "message",
		Action:       "send",
		TTL:          2 * time.Second,
	})

	err := oc.resolveToolApproval(
		roomID,
		approvalID,
		airuntime.ToolApprovalDecision{State: airuntime.ToolApprovalApproved},
		false,
		id.UserID("@attacker:example.com"),
	)
	if err == nil {
		t.Fatalf("expected non-owner resolution to fail")
	}
}

func TestToolApprovals_RejectCrossRoom(t *testing.T) {
	owner := id.UserID("@owner:example.com")
	roomID := id.RoomID("!room1:example.com")
	otherRoom := id.RoomID("!room2:example.com")

	oc := newTestAIClient(owner)
	approvalID := "approval-1"
	oc.registerToolApproval(ToolApprovalParams{
		ApprovalID:   approvalID,
		RoomID:       roomID,
		TurnID:       "turn-1",
		ToolCallID:   "call-1",
		ToolName:     "message",
		ToolKind:     ToolApprovalKindBuiltin,
		RuleToolName: "message",
		Action:       "send",
		TTL:          2 * time.Second,
	})

	if err := oc.resolveToolApproval(
		otherRoom,
		approvalID,
		airuntime.ToolApprovalDecision{State: airuntime.ToolApprovalApproved},
		false,
		owner,
	); err == nil {
		t.Fatalf("expected cross-room resolution to fail")
	}
}

func TestToolApprovals_TimeoutAutoDeny(t *testing.T) {
	owner := id.UserID("@owner:example.com")
	roomID := id.RoomID("!room:example.com")

	oc := newTestAIClient(owner)
	approvalID := "approval-1"
	oc.registerToolApproval(ToolApprovalParams{
		ApprovalID:   approvalID,
		RoomID:       roomID,
		TurnID:       "turn-1",
		ToolCallID:   "call-1",
		ToolName:     "message",
		ToolKind:     ToolApprovalKindBuiltin,
		RuleToolName: "message",
		Action:       "send",
		TTL:          5 * time.Millisecond,
	})

	time.Sleep(15 * time.Millisecond)
	_, _, ok := oc.waitToolApproval(context.Background(), approvalID)
	if ok {
		t.Fatalf("expected timeout (ok=false)")
	}
}
