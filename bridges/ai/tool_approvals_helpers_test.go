package ai

import (
	"context"
	"testing"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote"
	bridgesdk "github.com/beeper/agentremote/sdk"
)

func TestApprovalParamsFromRequestHandlesNilStateTurn(t *testing.T) {
	oc := &AIClient{}
	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: id.RoomID("!room:example.com")}}

	params := oc.approvalParamsFromRequest(portal, &streamingState{}, nil, bridgesdk.ApprovalRequest{
		ToolCallID: " call-1 ",
		ToolName:   " message ",
		Metadata: map[string]any{
			approvalMetadataKeyToolKind:     string(ToolApprovalKindBuiltin),
			approvalMetadataKeyRuleToolName: " message ",
			approvalMetadataKeyAction:       " send ",
		},
	})

	if params.ApprovalID == "" {
		t.Fatal("expected generated approval ID")
	}
	if params.RoomID != portal.MXID {
		t.Fatalf("expected room id %q, got %q", portal.MXID, params.RoomID)
	}
	if params.ToolCallID != "call-1" {
		t.Fatalf("expected trimmed tool call id, got %q", params.ToolCallID)
	}
	if params.ToolName != "message" {
		t.Fatalf("expected trimmed tool name, got %q", params.ToolName)
	}
	if params.ToolKind != ToolApprovalKindBuiltin {
		t.Fatalf("expected builtin kind, got %q", params.ToolKind)
	}
	if params.RuleToolName != "message" {
		t.Fatalf("expected trimmed rule tool name, got %q", params.RuleToolName)
	}
	if params.Action != "send" {
		t.Fatalf("expected trimmed action, got %q", params.Action)
	}
	if params.TTL != 10*time.Minute {
		t.Fatalf("expected default ttl 10m, got %v", params.TTL)
	}
}

func TestApprovalWaitReason(t *testing.T) {
	if got := approvalWaitReason(context.Background()); got != agentremote.ApprovalReasonTimeout {
		t.Fatalf("expected timeout reason, got %q", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := approvalWaitReason(ctx); got != agentremote.ApprovalReasonCancelled {
		t.Fatalf("expected cancelled reason, got %q", got)
	}
}
