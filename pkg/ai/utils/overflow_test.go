package utils

import (
	"testing"

	"github.com/beeper/ai-bridge/pkg/ai"
)

func TestIsContextOverflow_ErrorPattern(t *testing.T) {
	msg := ai.Message{
		Role:         ai.RoleAssistant,
		StopReason:   ai.StopReasonError,
		ErrorMessage: "prompt is too long: 213462 tokens > 200000 maximum",
	}
	if !IsContextOverflow(msg, 0) {
		t.Fatalf("expected context overflow for anthropic style error")
	}
}

func TestIsContextOverflow_SilentOverflow(t *testing.T) {
	msg := ai.Message{
		Role:       ai.RoleAssistant,
		StopReason: ai.StopReasonStop,
		Usage: ai.Usage{
			Input:     1200,
			CacheRead: 100,
		},
	}
	if !IsContextOverflow(msg, 1000) {
		t.Fatalf("expected silent overflow when usage exceeds context window")
	}
}
