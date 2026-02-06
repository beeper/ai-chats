package connector

import (
	"strings"
	"testing"
	"time"

	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/id"
)

func TestStripOpenClawToolResults(t *testing.T) {
	input := []map[string]any{
		{"role": "user"},
		{"role": "assistant"},
		{"role": "toolResult"},
	}
	got := stripOpenClawToolResults(input)
	if len(got) != 2 {
		t.Fatalf("expected 2 messages after strip, got %d", len(got))
	}
	for _, msg := range got {
		if msg["role"] == "toolResult" {
			t.Fatalf("toolResult message was not stripped")
		}
	}
}

func TestRepairOpenClawToolPairing(t *testing.T) {
	messages := []map[string]any{
		{
			"role": "assistant",
			"content": []map[string]any{
				{"type": "toolCall", "id": "call_1", "name": "Read"},
			},
		},
		{"role": "user", "content": []map[string]any{{"type": "text", "text": "interleaved"}}},
		{"role": "toolResult", "toolCallId": "call_1", "toolName": "Read"},
		{"role": "toolResult", "toolCallId": "call_1", "toolName": "Read"}, // duplicate
		{"role": "toolResult", "toolCallId": "call_orphan", "toolName": "Bash"},
		{"role": "assistant", "content": []map[string]any{{"type": "text", "text": "done"}}},
	}

	repaired := repairOpenClawToolPairing(messages)
	if len(repaired) != 4 {
		t.Fatalf("expected 4 messages after repair, got %d", len(repaired))
	}
	if repaired[0]["role"] != "assistant" {
		t.Fatalf("expected repaired[0] to be assistant, got %v", repaired[0]["role"])
	}
	if repaired[1]["role"] != "toolResult" {
		t.Fatalf("expected repaired[1] to be toolResult, got %v", repaired[1]["role"])
	}
	if repaired[2]["role"] != "user" {
		t.Fatalf("expected repaired[2] to be user, got %v", repaired[2]["role"])
	}
	if repaired[3]["role"] != "assistant" {
		t.Fatalf("expected repaired[3] to be assistant, got %v", repaired[3]["role"])
	}
	if repaired[1]["toolCallId"] != "call_1" {
		t.Fatalf("expected toolCallId call_1, got %v", repaired[1]["toolCallId"])
	}
}

func TestRepairOpenClawToolPairingInsertsSynthetic(t *testing.T) {
	messages := []map[string]any{
		{
			"role": "assistant",
			"content": []map[string]any{
				{"type": "toolCall", "id": "call_missing", "name": "Read"},
			},
		},
		{"role": "user", "content": []map[string]any{{"type": "text", "text": "next"}}},
	}

	repaired := repairOpenClawToolPairing(messages)
	if len(repaired) != 3 {
		t.Fatalf("expected 3 messages after repair, got %d", len(repaired))
	}
	if repaired[1]["role"] != "toolResult" {
		t.Fatalf("expected inserted toolResult at index 1, got %v", repaired[1]["role"])
	}
	if repaired[1]["toolCallId"] != "call_missing" {
		t.Fatalf("expected synthetic toolCallId call_missing, got %v", repaired[1]["toolCallId"])
	}
	if repaired[1]["isError"] != true {
		t.Fatalf("expected synthetic result to be error")
	}
	content, ok := repaired[1]["content"].([]map[string]any)
	if !ok || len(content) == 0 {
		t.Fatalf("expected synthetic content blocks")
	}
	text := content[0]["text"]
	if !strings.Contains(toString(text), "[openclaw] missing tool result") {
		t.Fatalf("unexpected synthetic text: %v", text)
	}
}

func TestBuildOpenClawSessionMessagesFromCanonical(t *testing.T) {
	msg := &database.Message{
		MXID:      id.EventID("$assistant1"),
		Timestamp: time.UnixMilli(1730000000000),
		Metadata: &MessageMetadata{
			Role:            "assistant",
			CanonicalSchema: "ai-sdk-ui-message-v1",
			CanonicalUIMessage: map[string]any{
				"parts": []any{
					map[string]any{"type": "text", "text": "hello"},
					map[string]any{
						"type":       "dynamic-tool",
						"toolCallId": "call_1",
						"toolName":   "web_search",
						"input":      map[string]any{"q": "matrix"},
						"state":      "output-available",
						"output":     map[string]any{"result": "ok"},
					},
				},
			},
			ToolCalls: []ToolCallMetadata{
				{
					CallID:        "call_1",
					ToolName:      "web_search",
					Input:         map[string]any{"q": "matrix"},
					Output:        map[string]any{"result": "ok"},
					ResultStatus:  "success",
					CallEventID:   "$toolcall1",
					ResultEventID: "$toolresult1",
				},
			},
		},
	}

	out := buildOpenClawSessionMessages([]*database.Message{msg}, true)
	if len(out) != 2 {
		t.Fatalf("expected assistant + toolResult, got %d messages", len(out))
	}
	if out[0]["role"] != "assistant" {
		t.Fatalf("expected first role assistant, got %v", out[0]["role"])
	}
	if out[1]["role"] != "toolResult" {
		t.Fatalf("expected second role toolResult, got %v", out[1]["role"])
	}
	if out[1]["toolCallId"] != "call_1" {
		t.Fatalf("expected toolCallId call_1, got %v", out[1]["toolCallId"])
	}
	if out[1]["id"] != "$toolresult1" {
		t.Fatalf("expected projected result id from metadata, got %v", out[1]["id"])
	}

	filtered := buildOpenClawSessionMessages([]*database.Message{msg}, false)
	if len(filtered) != 1 {
		t.Fatalf("expected toolResult filtered out when includeTools=false, got %d", len(filtered))
	}
	if filtered[0]["role"] != "assistant" {
		t.Fatalf("expected assistant after filtering, got %v", filtered[0]["role"])
	}
}
