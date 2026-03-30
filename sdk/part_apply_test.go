package sdk

import (
	"context"
	"testing"

	"maunium.net/go/mautrix/bridgev2"
)

func newPartApplyTestTurn() *Turn {
	conv := NewConversation(context.Background(), nil, nil, bridgev2.EventSender{}, &Config[*struct{}, *struct{}]{}, nil)
	return conv.StartTurn(context.Background(), &Agent{ID: "agent"}, nil)
}

func TestApplyStreamPartPreservesPreliminaryToolOutput(t *testing.T) {
	turn := newPartApplyTestTurn()

	ApplyStreamPart(turn, map[string]any{
		"type":             "tool-input-available",
		"toolCallId":       "call-1",
		"toolName":         "fetch",
		"input":            map[string]any{"url": "https://example.com"},
		"providerExecuted": true,
	}, PartApplyOptions{})
	ApplyStreamPart(turn, map[string]any{
		"type":             "tool-output-available",
		"toolCallId":       "call-1",
		"output":           map[string]any{"status": "running"},
		"providerExecuted": true,
		"preliminary":      true,
	}, PartApplyOptions{})

	ui := turn.UIState().UIMessage
	parts, _ := ui["parts"].([]any)
	if len(parts) != 1 {
		t.Fatalf("expected 1 UI part, got %#v", parts)
	}
	part, _ := parts[0].(map[string]any)
	if part["state"] != "output-available" {
		t.Fatalf("unexpected tool state: %#v", part)
	}
	if preliminary, _ := part["preliminary"].(bool); !preliminary {
		t.Fatalf("expected preliminary flag, got %#v", part)
	}
	output, _ := part["output"].(map[string]any)
	if output["status"] != "running" {
		t.Fatalf("unexpected preliminary output: %#v", output)
	}
}

func TestApplyStreamPartFinalOutputClearsPreliminaryFlag(t *testing.T) {
	turn := newPartApplyTestTurn()

	ApplyStreamPart(turn, map[string]any{
		"type":             "tool-input-available",
		"toolCallId":       "call-2",
		"toolName":         "fetch",
		"input":            map[string]any{"url": "https://example.com"},
		"providerExecuted": true,
	}, PartApplyOptions{})
	ApplyStreamPart(turn, map[string]any{
		"type":             "tool-output-available",
		"toolCallId":       "call-2",
		"output":           map[string]any{"status": "running"},
		"providerExecuted": true,
		"preliminary":      true,
	}, PartApplyOptions{})
	ApplyStreamPart(turn, map[string]any{
		"type":             "tool-output-available",
		"toolCallId":       "call-2",
		"output":           map[string]any{"status": 200},
		"providerExecuted": true,
	}, PartApplyOptions{})

	ui := turn.UIState().UIMessage
	parts, _ := ui["parts"].([]any)
	if len(parts) != 1 {
		t.Fatalf("expected 1 UI part, got %#v", parts)
	}
	part, _ := parts[0].(map[string]any)
	if preliminary, ok := part["preliminary"].(bool); ok && preliminary {
		t.Fatalf("did not expect preliminary flag after final output: %#v", part)
	}
	output, _ := part["output"].(map[string]any)
	if output["status"] != 200 {
		t.Fatalf("unexpected final output: %#v", output)
	}
}
