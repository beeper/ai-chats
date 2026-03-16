package ai

import (
	"context"
	"testing"

	"github.com/openai/openai-go/v3/responses"
)

func TestHandleResponseOutputItemDoneEmitsLateArrivingToolInput(t *testing.T) {
	oc := &AIClient{}
	state := newTestStreamingStateWithTurn()
	activeTools := newStreamToolRegistry()
	tool := &activeToolCall{
		registryKey: streamToolItemKey("item_123"),
		callID:      "call_123",
		itemID:      "item_123",
		toolName:    "web_search",
		toolType:    ToolTypeFunction,
	}
	activeTools.byKey[tool.registryKey] = tool
	activeTools.BindAlias(streamToolCallKey(tool.callID), tool)

	item := responses.ResponseOutputItemUnion{
		ID:        tool.itemID,
		CallID:    tool.callID,
		Type:      "function_call",
		Name:      tool.toolName,
		Arguments: `{"query":"matrix"}`,
	}

	oc.handleResponseOutputItemDone(context.Background(), nil, state, activeTools, item)

	if got := tool.input.String(); got != `{"query":"matrix"}` {
		t.Fatalf("expected late-arriving tool input to be recorded, got %q", got)
	}
}
