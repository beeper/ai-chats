package connector

import (
	"context"
	"testing"

	"github.com/beeper/ai-bridge/pkg/bridgeadapter"
)

func TestHistoryMessageBundle_LegacyAssistantFallback(t *testing.T) {
	oc := &AIClient{}
	bundle := oc.historyMessageBundle(context.Background(), &MessageMetadata{
		BaseMessageMetadata: bridgeadapter.BaseMessageMetadata{
			Role: "assistant",
			Body: "done",
			ToolCalls: []ToolCallMetadata{{
				CallID:   "call_1",
				ToolName: "Read",
				Input:    map[string]any{"path": "README.md"},
				Output:   map[string]any{"result": "ok"},
			}},
		},
	}, false)

	if len(bundle) != 2 {
		t.Fatalf("expected assistant bundle with tool output, got %d entries", len(bundle))
	}
	if bundle[0].OfAssistant == nil {
		t.Fatalf("expected first bundle entry to be assistant message")
	}
	if len(bundle[0].OfAssistant.ToolCalls) != 1 {
		t.Fatalf("expected assistant tool call to be preserved, got %d", len(bundle[0].OfAssistant.ToolCalls))
	}
	if bundle[1].OfTool == nil || bundle[1].OfTool.ToolCallID != "call_1" {
		t.Fatalf("expected tool output for call_1, got %#v", bundle[1].OfTool)
	}
}

func TestHistoryMessageBundle_CanonicalPartsSupportsMapSlices(t *testing.T) {
	oc := &AIClient{}
	bundle := oc.historyMessageBundle(context.Background(), &MessageMetadata{
		BaseMessageMetadata: bridgeadapter.BaseMessageMetadata{
			Role:            "assistant",
			CanonicalSchema: "ai-sdk-ui-message-v1",
			CanonicalUIMessage: map[string]any{
				"role": "assistant",
				"parts": []map[string]any{
					{"type": "text", "text": "hello"},
					{
						"type":       "dynamic-tool",
						"toolCallId": "call_1",
						"toolName":   "Read",
						"input":      map[string]any{"path": "README.md"},
						"state":      "output-available",
						"output":     map[string]any{"result": "ok"},
					},
				},
			},
		},
	}, false)

	if len(bundle) != 2 {
		t.Fatalf("expected canonical assistant bundle with tool output, got %d entries", len(bundle))
	}
	if bundle[0].OfAssistant == nil {
		t.Fatalf("expected first bundle entry to be assistant message")
	}
	if len(bundle[0].OfAssistant.ToolCalls) != 1 {
		t.Fatalf("expected assistant tool call parsed from []map parts, got %d", len(bundle[0].OfAssistant.ToolCalls))
	}
	if bundle[1].OfTool == nil || bundle[1].OfTool.ToolCallID != "call_1" {
		t.Fatalf("expected tool output for call_1, got %#v", bundle[1].OfTool)
	}
}
