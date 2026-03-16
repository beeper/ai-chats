package ai

import (
	"context"
	"testing"

	"github.com/rs/zerolog"
)

func TestProcessStreamingTextDeltaEmitsPlainVisibleTextWithoutDirectives(t *testing.T) {
	oc := &AIClient{}
	state := newTestStreamingStateWithTurn()
	state.turn.SetSuppressSend(true)

	roundDelta, err := oc.processStreamingTextDelta(
		context.Background(),
		zerolog.Nop(),
		nil,
		state,
		nil,
		nil,
		false,
		"hello",
		"stream failed",
		"stream failed",
	)
	if err != nil {
		t.Fatalf("processStreamingTextDelta returned error: %v", err)
	}
	if roundDelta != "hello" {
		t.Fatalf("expected round delta hello, got %q", roundDelta)
	}
	if got := visibleStreamingText(state); got != "hello" {
		t.Fatalf("expected visible text hello, got %q", got)
	}
}

func TestDisplayStreamingTextPrefersVisibleTextOverRawAccumulated(t *testing.T) {
	oc := &AIClient{}
	state := newTestStreamingStateWithTurn()
	state.turn.SetSuppressSend(true)

	if _, err := oc.processStreamingTextDelta(
		context.Background(),
		zerolog.Nop(),
		nil,
		state,
		nil,
		nil,
		false,
		"[[reply_to_current]] visible",
		"stream failed",
		"stream failed",
	); err != nil {
		t.Fatalf("processStreamingTextDelta returned error: %v", err)
	}

	if got := rawStreamingText(state); got != "[[reply_to_current]] visible" {
		t.Fatalf("expected raw accumulated text to keep directives, got %q", got)
	}
	if got := displayStreamingText(state); got != "visible" {
		t.Fatalf("expected display text to strip directives, got %q", got)
	}
}
