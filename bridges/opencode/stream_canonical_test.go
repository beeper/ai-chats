package opencode

import "testing"

func TestCurrentUIMessageFallbackIncludesModelAndUsage(t *testing.T) {
	oc := &OpenCodeClient{}
	state := &openCodeStreamState{
		turnID:           "turn-1",
		agentID:          "agent-1",
		modelID:          "gpt-4.1",
		promptTokens:     11,
		completionTokens: 7,
		reasoningTokens:  3,
		totalTokens:      21,
	}
	state.stream.SetFinishReason("stop")
	state.stream.SetStartedAtMs(1000)
	state.stream.SetCompletedAtMs(2000)
	ui := oc.currentUIMessage(state)

	metadata, ok := ui["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("expected metadata map, got %T", ui["metadata"])
	}
	if metadata["model"] != "gpt-4.1" {
		t.Fatalf("expected model metadata, got %#v", metadata["model"])
	}
	usage, ok := metadata["usage"].(map[string]any)
	if !ok {
		t.Fatalf("expected usage metadata, got %T", metadata["usage"])
	}
	if usage["total_tokens"] != int64(21) {
		t.Fatalf("expected total_tokens 21, got %#v", usage["total_tokens"])
	}
}
