package connector

import (
	"context"
	"testing"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/id"
)

func TestHandleMatrixReaction_ApprovesByRelatesToEventID(t *testing.T) {
	owner := id.UserID("@owner:example.com")
	roomID := id.RoomID("!room:example.com")
	targetEvent := id.EventID("$toolcall:example.com")

	oc := newTestAIClient(owner)
	approvalID := "approval-1"
	oc.registerToolApproval(struct {
		ApprovalID string
		RoomID     id.RoomID
		TurnID     string

		ToolCallID string
		ToolName   string

		ToolKind     ToolApprovalKind
		RuleToolName string
		ServerLabel  string
		Action       string
		TargetEvent  id.EventID

		TTL time.Duration
	}{
		ApprovalID:   approvalID,
		RoomID:       roomID,
		TurnID:       "turn-1",
		ToolCallID:   "call-1",
		ToolName:     "message",
		ToolKind:     ToolApprovalKindBuiltin,
		RuleToolName: "message",
		Action:       "send",
		TargetEvent:  targetEvent,
		TTL:          2 * time.Second,
	})

	portal := &bridgev2.Portal{
		Portal: &database.Portal{
			MXID:     roomID,
			Metadata: &PortalMetadata{},
		},
	}

	msg := &bridgev2.MatrixReaction{
		MatrixEventBase: bridgev2.MatrixEventBase[*event.ReactionEventContent]{
			Event: &event.Event{
				Sender:    owner,
				Timestamp: time.Now().UnixMilli(),
			},
			Portal: portal,
			Content: &event.ReactionEventContent{
				RelatesTo: event.RelatesTo{
					Type:    event.RelAnnotation,
					EventID: targetEvent,
					Key:     "👍",
				},
			},
		},
	}

	if _, err := oc.HandleMatrixReaction(context.Background(), msg); err != nil {
		t.Fatalf("HandleMatrixReaction returned error: %v", err)
	}

	decision, _, ok := oc.waitToolApproval(context.Background(), approvalID)
	if !ok {
		t.Fatalf("expected wait ok")
	}
	if !decision.Approve {
		t.Fatalf("expected approve=true")
	}
}
