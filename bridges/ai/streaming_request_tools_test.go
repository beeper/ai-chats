package ai

import (
	"context"
	"slices"
	"testing"
)

func TestSelectedStreamingToolDescriptorsSkipsAllToolsWhenModelCannotCallTools(t *testing.T) {
	meta := modelModeTestMeta("openai/gpt-5.2")

	withTools := testBuiltinToolClient(true, true, true).selectedStreamingToolDescriptors(context.Background(), meta, false)
	if len(withTools) == 0 {
		t.Fatal("expected tool descriptors when tool calling is supported")
	}

	withoutTools := testBuiltinToolClient(false, true, true).selectedStreamingToolDescriptors(context.Background(), meta, false)
	if len(withoutTools) != 0 {
		t.Fatalf("expected no tool descriptors when tool calling is unsupported, got %#v", withoutTools)
	}
}

func TestBuildResponsesAgentLoopParams_ModelRoomUsesModelPreset(t *testing.T) {
	meta := modelModeTestMeta("openai/gpt-5.2")
	client := testBuiltinToolClient(true, true, true)

	params := client.buildResponsesAgentLoopParams(context.Background(), meta, "system prompt", nil, false)
	got := responsesToolNames(params.Tools)
	want := []string{toolNameSessionStatus, toolNameWebFetch, ToolNameWebSearch}
	if !slices.Equal(got, want) {
		t.Fatalf("unexpected response tool list: got %v want %v", got, want)
	}
}
