package ai

import (
	"context"
	"testing"

	"github.com/openai/openai-go/v3/responses"
	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote/sdk"
)

func TestParseJSONOrRaw_EmptyStringReturnsNil(t *testing.T) {
	if got := parseJSONOrRaw(""); got != nil {
		t.Fatalf("expected nil for empty input, got %#v", got)
	}
}

func TestDeriveToolDescriptorForOutputItem_FunctionCallLeavesBlankArgumentsEmpty(t *testing.T) {
	desc := deriveToolDescriptorForOutputItem(responses.ResponseOutputItemUnion{
		ID:        "item_123",
		CallID:    "call_123",
		Type:      "function_call",
		Name:      "web_search",
		Arguments: "",
	}, nil)

	if !desc.ok {
		t.Fatal("expected descriptor to be valid")
	}
	if desc.input != nil {
		t.Fatalf("expected blank arguments to remain nil, got %#v", desc.input)
	}
}

func TestDeriveToolDescriptorForOutputItem_FunctionCallParsesArgumentsJSON(t *testing.T) {
	desc := deriveToolDescriptorForOutputItem(responses.ResponseOutputItemUnion{
		ID:        "item_123",
		CallID:    "call_123",
		Type:      "function_call",
		Name:      "web_search",
		Arguments: "{\"query\":\"latest news headlines today\",\"count\":10}",
	}, nil)

	if !desc.ok {
		t.Fatal("expected descriptor to be valid")
	}
	input, ok := desc.input.(map[string]any)
	if !ok {
		t.Fatalf("expected parsed argument map, got %#v", desc.input)
	}
	if got := input["query"]; got != "latest news headlines today" {
		t.Fatalf("expected query to be preserved, got %#v", got)
	}
	if got := input["count"]; got != float64(10) {
		t.Fatalf("expected count 10, got %#v", got)
	}
}

func TestUpsertActiveToolFromDescriptor_RecreatesNilMapEntry(t *testing.T) {
	oc := &AIClient{}
	state := newStreamingState(context.Background(), nil, "")
	conv := sdk.NewConversation[*AIClient, *Config](context.Background(), nil, nil, bridgev2.EventSender{}, nil, nil)
	state.turn = conv.StartTurn(context.Background(), nil, nil)
	activeTools := newStreamToolRegistry()
	activeTools.byKey[streamToolItemKey("item_123")] = nil

	tool, created := oc.upsertActiveToolFromDescriptor(context.Background(), nil, state, activeTools, responseToolDescriptor{
		ok:          true,
		registryKey: streamToolItemKey("item_123"),
		itemID:      "item_123",
		callID:      "call_123",
		toolName:    "web_search",
		toolType:    ToolTypeFunction,
	})
	if !created {
		t.Fatalf("expected nil map entry to be recreated")
	}
	if tool == nil {
		t.Fatal("expected tool to be recreated")
	}
	if activeTools.Lookup(streamToolItemKey("item_123")) == nil {
		t.Fatal("expected recreated tool to be stored back into the map")
	}
	if tool.callID == "" || tool.toolName != "web_search" {
		t.Fatalf("expected recreated tool to be populated, got %#v", tool)
	}
}
