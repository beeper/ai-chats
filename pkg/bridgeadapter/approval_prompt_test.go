package bridgeadapter

import (
	"strings"
	"testing"
	"time"

	"maunium.net/go/mautrix/id"
)

func TestBuildApprovalPromptMessage_UsesStructuredPresentationAndMetadata(t *testing.T) {
	msg := BuildApprovalPromptMessage(ApprovalPromptMessageParams{
		ApprovalID: "approval-1",
		ToolCallID: "tool-1",
		ToolName:   "message",
		TurnID:     "turn-1",
		Presentation: ApprovalPromptPresentation{
			Title:       "Send message",
			AllowAlways: false,
			Details: []ApprovalDetail{
				{Label: "Tool", Value: "message"},
				{Label: "Action", Value: "send"},
			},
		},
		ExpiresAt: time.UnixMilli(12345),
	})
	if !strings.Contains(msg.Body, "Approval required: Send message") {
		t.Fatalf("expected title in body, got %q", msg.Body)
	}
	if !strings.Contains(msg.Body, "Tool: message") || !strings.Contains(msg.Body, "Action: send") {
		t.Fatalf("expected details in body, got %q", msg.Body)
	}
	if strings.Contains(msg.Body, "Always allow") {
		t.Fatalf("did not expect always allow in body when AllowAlways=false, got %q", msg.Body)
	}
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
	if rendered, ok := approvalRaw["renderedOptions"].([]string); !ok || len(rendered) != 2 {
		t.Fatalf("expected two rendered options, got %#v", approvalRaw["renderedOptions"])
	}
	presentationRaw, ok := approvalRaw["presentation"].(map[string]any)
	if !ok {
		t.Fatalf("expected presentation metadata, got %#v", approvalRaw["presentation"])
	}
	if presentationRaw["title"] != "Send message" {
		t.Fatalf("expected presentation title, got %#v", presentationRaw["title"])
	}
}

func TestApprovalPromptOptions_AllowAlwaysSwitch(t *testing.T) {
	if got := ApprovalPromptOptions(false); len(got) != 2 {
		t.Fatalf("expected 2 options when AllowAlways=false, got %d", len(got))
	}
	if got := ApprovalPromptOptions(true); len(got) != 3 {
		t.Fatalf("expected 3 options when AllowAlways=true, got %d", len(got))
	}
}

func TestBuildApprovalResponsePromptMessage_ContainsDecision(t *testing.T) {
	msg := BuildApprovalResponsePromptMessage(ApprovalResponsePromptMessageParams{
		ApprovalID: "approval-1",
		ToolCallID: "tool-1",
		ToolName:   "message",
		TurnID:     "turn-1",
		Presentation: ApprovalPromptPresentation{
			Title: "Send message",
		},
		Decision: ApprovalDecisionPayload{
			ApprovalID: "approval-1",
			Approved:   false,
			Reason:     "deny",
		},
	})
	approvalRaw, ok := msg.Raw[ApprovalDecisionKey].(map[string]any)
	if !ok {
		t.Fatalf("expected approval metadata map")
	}
	if approvalRaw["kind"] != "response" {
		t.Fatalf("expected response kind, got %#v", approvalRaw["kind"])
	}
	if approvalRaw["approved"] != false {
		t.Fatalf("expected approved=false, got %#v", approvalRaw["approved"])
	}
	if approvalRaw["reason"] != "deny" {
		t.Fatalf("expected reason=deny, got %#v", approvalRaw["reason"])
	}
	uiParts, _ := msg.UIMessage["parts"].([]map[string]any)
	if len(uiParts) != 1 {
		t.Fatalf("expected one ui part, got %#v", msg.UIMessage["parts"])
	}
	if uiParts[0]["state"] != ApprovalPromptStateResponded {
		t.Fatalf("expected responded state, got %#v", uiParts[0]["state"])
	}
	approval, _ := uiParts[0]["approval"].(map[string]any)
	if approval["approved"] != false || approval["reason"] != "deny" {
		t.Fatalf("expected approval payload with approved=false reason=deny, got %#v", approval)
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
