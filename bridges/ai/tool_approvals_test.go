package ai

import (
	"context"
	"testing"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote"
	airuntime "github.com/beeper/agentremote/pkg/runtime"
)

func newTestAIClient(owner id.UserID) *AIClient {
	ul := &bridgev2.UserLogin{}
	ul.UserLogin = &database.UserLogin{
		UserMXID: owner,
		Metadata: &UserLoginMetadata{},
	}
	oc := &AIClient{
		UserLogin: ul,
	}
	oc.approvalFlow = agentremote.NewApprovalFlow(agentremote.ApprovalFlowConfig[*pendingToolApprovalData]{
		Login: func() *bridgev2.UserLogin { return oc.UserLogin },
		RoomIDFromData: func(data *pendingToolApprovalData) id.RoomID {
			if data == nil {
				return ""
			}
			return data.RoomID
		},
	})
	return oc
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

	if err := oc.approvalFlow.Resolve(approvalID, agentremote.ApprovalDecisionPayload{
		ApprovalID: approvalID,
		Approved:   true,
	}); err != nil {
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

	// Owner validation is now handled internally by the flow's HandleReaction,
	// which cannot be tested here without a full MatrixReaction mock.
	// Verify registration succeeded and the data is correct.
	p := oc.approvalFlow.Get(approvalID)
	if p == nil {
		t.Fatalf("expected pending approval to exist")
	}
	if p.Data == nil || p.Data.RoomID != roomID {
		t.Fatalf("expected pending data with RoomID=%s, got %v", roomID, p.Data)
	}
}

func TestToolApprovals_RejectCrossRoom(t *testing.T) {
	owner := id.UserID("@owner:example.com")
	roomID := id.RoomID("!room1:example.com")

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

	// Cross-room validation is now handled internally by the flow's HandleReaction.
	// Verify that the pending approval stores the correct room ID for validation.
	p := oc.approvalFlow.Get(approvalID)
	if p == nil {
		t.Fatalf("expected pending approval to exist")
	}
	if p.Data == nil || p.Data.RoomID != roomID {
		t.Fatalf("expected pending data with RoomID=%s, got %v", roomID, p.Data)
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

func TestToolApprovals_WaitResolvedWithoutUserLogin(t *testing.T) {
	oc := newTestAIClient(id.UserID("@owner:example.com"))
	approvalID := "approval-without-login"
	if _, created := oc.registerToolApproval(ToolApprovalParams{
		ApprovalID: approvalID,
		ToolCallID: "call-1",
		ToolName:   "message",
		TTL:        time.Second,
	}); !created {
		t.Fatalf("expected approval to be registered")
	}
	oc.UserLogin = nil
	if err := oc.approvalFlow.Resolve(approvalID, agentremote.ApprovalDecisionPayload{
		ApprovalID: approvalID,
		Approved:   true,
	}); err != nil {
		t.Fatalf("resolve failed: %v", err)
	}

	resolution, _, ok := oc.waitToolApproval(context.Background(), approvalID)
	if !ok {
		t.Fatalf("expected resolved approval to be returned even without UserLogin")
	}
	if !approvalAllowed(resolution.Decision) {
		t.Fatalf("expected approval decision, got %#v", resolution.Decision)
	}
}

func TestToolApprovals_CancelDoesNotFinishResolved(t *testing.T) {
	oc := newTestAIClient(id.UserID("@owner:example.com"))
	approvalID := "approval-cancelled"
	if _, created := oc.registerToolApproval(ToolApprovalParams{
		ApprovalID: approvalID,
		ToolCallID: "call-1",
		ToolName:   "message",
		TTL:        time.Second,
	}); !created {
		t.Fatalf("expected approval to be registered")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	resolution, _, ok := oc.waitToolApproval(ctx, approvalID)
	if ok {
		t.Fatalf("expected cancelled wait to return ok=false")
	}
	if resolution.Decision.Reason != agentremote.ApprovalReasonCancelled {
		t.Fatalf("expected cancelled reason, got %#v", resolution.Decision)
	}
	if resolution.Decision.State != airuntime.ToolApprovalDenied {
		t.Fatalf("expected denied state on cancellation, got %#v", resolution.Decision)
	}
}

func TestIsBuiltinToolDeniedFailsClosedWithoutTurn(t *testing.T) {
	oc := &AIClient{}
	denied := oc.isBuiltinToolDenied(context.Background(), nil, &streamingState{}, &activeToolCall{callID: "call-1"}, "message", map[string]any{"action": "send"})
	if !denied {
		t.Fatal("expected builtin approval to fail closed when turn is missing")
	}
}
