package connector

import (
	"testing"

	runtimeparse "github.com/beeper/ai-bridge/pkg/runtime"
)

func TestStripEnvelope(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"[WebChat user Thu 2025-01-02T03:04Z] hello", "hello"},
		{"[WhatsApp 2026-01-24 13:36] hello world", "hello world"},
		{"[WhatsApp Alice +2m 2024-01-01 10:00] how are you?", "how are you?"},
		{"[Telegram Bot] test message", "test message"},
		{"[Signal user] hi", "hi"},
		{"[Slack user] msg", "msg"},
		{"[Discord user] msg", "msg"},
		{"[iMessage user] msg", "msg"},
		{"[Matrix user] msg", "msg"},
		{"[Teams user] msg", "msg"},
		{"[Google Chat user] msg", "msg"},
		{"[WebChat user] msg", "msg"},
		{"[Zalo Personal sender] msg", "msg"},
		{"no envelope here", "no envelope here"},
		{"", ""},
		{"[WebChat] should not strip", "[WebChat] should not strip"},
		{"[Matrix] should not strip", "[Matrix] should not strip"},
		{"[Unknown prefix] should not strip", "[Unknown prefix] should not strip"},
	}
	for _, tt := range tests {
		if got := runtimeparse.StripEnvelope(tt.input); got != tt.want {
			t.Errorf("StripEnvelope(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
