package connector

import "testing"

func TestStripEnvelope(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"[Desktop API user 2024-01-01T00:00:00.000Z] hello", "hello"},
		{"[Desktop user +5m 2024-01-01T00:00:00.000Z] hello world", "hello world"},
		{"[WhatsApp Alice +2m] how are you?", "how are you?"},
		{"[Telegram Bot] test message", "test message"},
		{"[Signal user] hi", "hi"},
		{"[Slack user] msg", "msg"},
		{"[Discord user] msg", "msg"},
		{"[iMessage user] msg", "msg"},
		{"[Matrix user] msg", "msg"},
		{"[Teams user] msg", "msg"},
		{"[SMS user] msg", "msg"},
		{"[Google Chat user] msg", "msg"},
		{"[Channel user] msg", "msg"},
		{"[WebChat user] msg", "msg"},
		{"no envelope here", "no envelope here"},
		{"", ""},
		{"[Unknown prefix] should not strip", "[Unknown prefix] should not strip"},
	}
	for _, tt := range tests {
		if got := StripEnvelope(tt.input); got != tt.want {
			t.Errorf("StripEnvelope(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
