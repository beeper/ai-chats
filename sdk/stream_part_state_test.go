package sdk

import (
	"testing"
	"time"
)

func TestStreamPartStateAppliesTextAndReasoning(t *testing.T) {
	var state StreamPartState
	ts := time.Now()

	state.ApplyPart(map[string]any{"type": "text-delta", "delta": "hello"}, ts)
	state.ApplyPart(map[string]any{"type": "reasoning-delta", "delta": "thinking"}, ts.Add(time.Millisecond))

	if got := state.VisibleText(); got != "hello" {
		t.Fatalf("expected visible text hello, got %q", got)
	}
	if got := state.AccumulatedText(); got != "hellothinking" {
		t.Fatalf("expected accumulated text, got %q", got)
	}
	if state.StartedAtMs() == 0 || state.FirstTokenAtMs() == 0 {
		t.Fatalf("expected lifecycle timestamps, got started=%d first=%d", state.StartedAtMs(), state.FirstTokenAtMs())
	}
}

func TestStreamPartStateAppliesTerminalFields(t *testing.T) {
	var state StreamPartState
	ts := time.Now()

	state.ApplyPart(map[string]any{"type": "error", "errorText": "boom"}, ts)
	if state.ErrorText() != "boom" {
		t.Fatalf("expected error text boom, got %q", state.ErrorText())
	}
	if state.CompletedAtMs() == 0 {
		t.Fatal("expected completed timestamp")
	}

	state.ApplyPart(map[string]any{"type": "abort"}, ts.Add(time.Millisecond))
	if state.FinishReason() != "aborted" {
		t.Fatalf("expected aborted finish reason, got %q", state.FinishReason())
	}

	state.ApplyPart(map[string]any{"type": "finish", "finishReason": "stop"}, ts.Add(2*time.Millisecond))
	if state.FinishReason() != "stop" {
		t.Fatalf("expected stop finish reason, got %q", state.FinishReason())
	}
}
