package streamui

import "testing"

func TestApplyChunkToolApprovalResponse(t *testing.T) {
	state := &UIState{TurnID: "turn-1"}
	ApplyChunk(state, map[string]any{
		"type":       "tool-input-available",
		"toolCallId": "call-1",
		"toolName":   "exec",
		"input":      map[string]any{"command": "ls"},
	})
	ApplyChunk(state, map[string]any{
		"type":       "tool-approval-request",
		"approvalId": "approval-1",
		"toolCallId": "call-1",
	})
	ApplyChunk(state, map[string]any{
		"type":       "tool-approval-response",
		"approvalId": "approval-1",
		"toolCallId": "call-1",
		"approved":   false,
		"reason":     "deny",
	})

	message := SnapshotCanonicalUIMessage(state)
	parts, _ := message["parts"].([]any)
	if len(parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(parts))
	}
	part, _ := parts[0].(map[string]any)
	approval, _ := part["approval"].(map[string]any)
	if part["state"] != "approval-responded" {
		t.Fatalf("unexpected tool state: %#v", part["state"])
	}
	if approval["approved"] != false {
		t.Fatalf("expected approved=false, got %#v", approval["approved"])
	}
	if approval["reason"] != "deny" {
		t.Fatalf("expected reason=deny, got %#v", approval["reason"])
	}
}
