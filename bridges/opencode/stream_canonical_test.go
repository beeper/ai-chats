package opencode

import (
	"testing"

	"github.com/beeper/agentremote/bridges/opencode/api"
)

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

func TestOpenCodeMessageStreamTurnIDRequiresSessionAndMessage(t *testing.T) {
	if got := opencodeMessageStreamTurnID("session-1", "message-1"); got != "opencode-msg-session-1-message-1" {
		t.Fatalf("unexpected turn id: %q", got)
	}
	if got := opencodeMessageStreamTurnID("", "message-1"); got != "" {
		t.Fatalf("expected empty turn id when session is missing, got %q", got)
	}
	if got := opencodeMessageStreamTurnID("session-1", ""); got != "" {
		t.Fatalf("expected empty turn id when message is missing, got %q", got)
	}
}

func TestPartTurnIDRequiresSessionAndMessage(t *testing.T) {
	part := api.Part{SessionID: "session-1", MessageID: "message-1"}
	if got := partTurnID(part); got != "opencode-msg-session-1-message-1" {
		t.Fatalf("unexpected part turn id: %q", got)
	}
	if got := partTurnID(api.Part{MessageID: "message-1"}); got != "" {
		t.Fatalf("expected empty part turn id when session is missing, got %q", got)
	}
}
