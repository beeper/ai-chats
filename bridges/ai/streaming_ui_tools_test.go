package ai

import (
	"context"
	"testing"
	"time"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote"
	bridgesdk "github.com/beeper/agentremote/sdk"
)

func TestRequestTurnApprovalWithoutApprovalFlowReturnsHandle(t *testing.T) {
	oc := &AIClient{}

	handle := oc.requestTurnApproval(context.Background(), nil, nil, nil, bridgesdk.ApprovalRequest{
		ApprovalID:   "approval-1",
		ToolCallID:   "tool-call-1",
		ToolName:     "tool",
		TTL:          60,
		Presentation: &agentremote.ApprovalPromptPresentation{Title: "Prompt"},
	})
	if handle == nil {
		t.Fatal("expected approval handle")
	}
	if handle.ID() != "approval-1" {
		t.Fatalf("expected approval id to round-trip, got %q", handle.ID())
	}
	if handle.ToolCallID() != "tool-call-1" {
		t.Fatalf("expected tool call id to round-trip, got %q", handle.ToolCallID())
	}

	resp, err := handle.Wait(context.Background())
	if err != nil {
		t.Fatalf("unexpected wait error: %v", err)
	}
	if resp.Approved {
		t.Fatal("expected approval to be denied without an approval flow")
	}
	if resp.Reason != agentremote.ApprovalReasonTimeout {
		t.Fatalf("expected timeout reason without approval flow, got %q", resp.Reason)
	}
}

func TestStartStreamingMCPApprovalAutoApprovedEmitsApprovalRequest(t *testing.T) {
	oc := newTestAIClient("@owner:example.com")
	state := newStreamingState(context.Background(), nil, "")
	conv := bridgesdk.NewConversation(context.Background(), nil, nil, bridgev2.EventSender{}, nil, nil)
	state.turn = conv.StartTurn(context.Background(), nil, nil)

	handle, err := oc.startStreamingMCPApproval(context.Background(), nil, state, ToolApprovalParams{
		ApprovalID:   "approval-1",
		ToolCallID:   "tool-call-1",
		ToolName:     "mcp.read_file",
		ToolKind:     ToolApprovalKindMCP,
		RuleToolName: "read_file",
		ServerLabel:  "filesystem",
		Presentation: agentremote.ApprovalPromptPresentation{Title: "Read file"},
		TTL:          time.Minute,
	}, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if handle == nil {
		t.Fatal("expected approval handle")
	}

	uiState := state.turn.UIState()
	if !uiState.UIToolApprovalRequested["approval-1"] {
		t.Fatal("expected auto-approved MCP request to mark approval requested")
	}
	if got := uiState.UIToolCallIDByApproval["approval-1"]; got != "tool-call-1" {
		t.Fatalf("expected approval to map to tool call, got %q", got)
	}

	resp, err := handle.Wait(context.Background())
	if err != nil {
		t.Fatalf("unexpected wait error: %v", err)
	}
	if !resp.Approved {
		t.Fatal("expected auto-approved MCP request to resolve as approved")
	}
	if resp.Reason != agentremote.ApprovalReasonAutoApproved {
		t.Fatalf("expected auto-approved reason, got %q", resp.Reason)
	}
}

func TestBuildStreamUIMessageIncludesPendingApprovalState(t *testing.T) {
	oc := newTestAIClient("@owner:example.com")
	state := newTestStreamingStateWithTurn()
	state.turn.SetSuppressSend(true)
	state.writer().Tools().EnsureInputStart(context.Background(), "tool-call-1", nil, bridgesdk.ToolInputOptions{
		ToolName:         "mcp.read_file",
		ProviderExecuted: true,
		DisplayTitle:     "Read file",
	})

	handle, err := oc.startStreamingMCPApproval(context.Background(), nil, state, ToolApprovalParams{
		ApprovalID:   "approval-1",
		ToolCallID:   "tool-call-1",
		ToolName:     "mcp.read_file",
		ToolKind:     ToolApprovalKindMCP,
		RuleToolName: "read_file",
		ServerLabel:  "filesystem",
		Presentation: agentremote.ApprovalPromptPresentation{Title: "Read file"},
		TTL:          time.Minute,
	}, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if handle == nil {
		t.Fatal("expected approval handle")
	}

	ui := oc.buildStreamUIMessage(state, nil, nil)
	if ui == nil {
		t.Fatal("expected canonical UI message")
	}

	rawParts, ok := ui["parts"].([]any)
	if !ok {
		t.Fatalf("expected parts array, got %T", ui["parts"])
	}

	found := false
	for _, rawPart := range rawParts {
		part, ok := rawPart.(map[string]any)
		if !ok {
			continue
		}
		if part["type"] != "tool" || part["toolCallId"] != "tool-call-1" {
			continue
		}
		if part["state"] != "approval-requested" {
			t.Fatalf("expected pending approval state, got %#v", part["state"])
		}
		approval, _ := part["approval"].(map[string]any)
		if approval["id"] != "approval-1" {
			t.Fatalf("expected approval id in persisted UI message, got %#v", approval["id"])
		}
		found = true
	}
	if !found {
		t.Fatal("expected persisted UI message to include the pending approval tool part")
	}
}
