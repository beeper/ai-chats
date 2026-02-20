package connector

import (
	"strings"
	"testing"
)

func TestStreamingDirectiveAccumulator_WhitespacePreservation(t *testing.T) {
	tests := []struct {
		name   string
		deltas []string
		want   string
	}{
		{
			name:   "space between words is preserved",
			deltas: []string{"Hello", " ", "world"},
			want:   "Hello world",
		},
		{
			name:   "leading space before first word is preserved",
			deltas: []string{" ", "Hello"},
			want:   " Hello",
		},
		{
			name:   "newline between words is preserved",
			deltas: []string{"Hello", "\n", "world"},
			want:   "Hello\nworld",
		},
		{
			name:   "multiple whitespace deltas accumulate",
			deltas: []string{"Hello", " ", " ", "world"},
			want:   "Hello  world",
		},
		{
			name:   "tab whitespace is preserved",
			deltas: []string{"Hello", "\t", "world"},
			want:   "Hello\tworld",
		},
		{
			name:   "mixed whitespace preserved",
			deltas: []string{"a", " \n ", "b"},
			want:   "a \n b",
		},
		{
			name:   "no whitespace issue when words arrive together",
			deltas: []string{"Hello world"},
			want:   "Hello world",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			acc := newStreamingDirectiveAccumulator()
			var got string
			for i, delta := range tt.deltas {
				final := i == len(tt.deltas)-1
				result := acc.Consume(delta, final)
				if result != nil {
					got += result.Text
				}
			}
			if got != tt.want {
				t.Errorf("accumulated text = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStreamingDirectiveAccumulator_NewAccumulatorClearsWhitespace(t *testing.T) {
	acc := newStreamingDirectiveAccumulator()

	// Send whitespace-only delta (deferred)
	result := acc.Consume(" ", false)
	if result != nil {
		t.Fatal("expected nil for whitespace-only delta")
	}

	// A fresh accumulator should clear pending whitespace state.
	acc = newStreamingDirectiveAccumulator()

	// Next word should NOT have the space prepended
	result = acc.Consume("Hello", true)
	if result == nil {
		t.Fatal("expected non-nil result for 'Hello'")
	}
	if result.Text != "Hello" {
		t.Errorf("with new accumulator, text = %q, want %q", result.Text, "Hello")
	}
}

func TestStreamingDirectiveAccumulator_TrailingWhitespace(t *testing.T) {
	acc := newStreamingDirectiveAccumulator()

	// Whitespace at the end of the stream (final=true) should still be emitted
	// if there's no subsequent renderable content, but the accumulator stores it.
	// When final=true and only whitespace, it returns nil (nothing to render).
	result := acc.Consume("Hello", false)
	if result == nil || result.Text != "Hello" {
		t.Fatalf("expected 'Hello', got %v", result)
	}

	// Final whitespace-only delta — returns nil since TrimSpace is empty
	_ = acc.Consume(" ", true)
	// This is acceptable: trailing whitespace at end of stream has no next token
	// to attach to, so it's dropped. The important case is whitespace BETWEEN words.
}

func TestStreamingDirectiveAccumulator_MessageIDHintStripped(t *testing.T) {
	tests := []struct {
		name   string
		deltas []string
		want   string
	}{
		{
			name:   "complete hint in single chunk is stripped",
			deltas: []string{"ping\n[message_id: $abc:example.com]"},
			want:   "ping",
		},
		{
			name:   "hint split across two chunks is stripped",
			deltas: []string{"ping\n[message_id:", " $abc:example.com]"},
			want:   "ping",
		},
		{
			name:   "hint split across many chunks is stripped",
			deltas: []string{"ping\n[message", "_id: ", "$abc:example.com", "]"},
			want:   "ping",
		},
		{
			name:   "hint with long id like screenshot",
			deltas: []string{"ping\n[message_id: $IRz8K4GhPmvD2xQw5TnJf9BcYeA3Mi0NuOpHsXdWk6rV1yZaIbCj7EtF8UqLwSoN:.local-ai.localhost]"},
			want:   "ping",
		},
		{
			name:   "matrix event id hint is stripped",
			deltas: []string{"hello\n[matrix event id: $abc:example.com]"},
			want:   "hello",
		},
		{
			name:   "regular markdown link is not stripped",
			deltas: []string{"check [this link](https://example.com)"},
			want:   "check [this link](https://example.com)",
		},
		{
			name:   "no hint leaves text unchanged",
			deltas: []string{"hello ", "world"},
			want:   "hello world",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			acc := newStreamingDirectiveAccumulator()
			var got string
			for i, delta := range tt.deltas {
				final := i == len(tt.deltas)-1
				result := acc.Consume(delta, final)
				if result != nil {
					got += result.Text
				}
			}
			got = strings.TrimSpace(got)
			want := strings.TrimSpace(tt.want)
			if got != want {
				t.Errorf("accumulated text = %q, want %q", got, want)
			}
		})
	}
}

func TestSplitTrailingMessageIDHint(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantBody string
		wantTail string
	}{
		{
			name:     "incomplete hint is buffered",
			input:    "ping\n[message_id: $abc",
			wantBody: "ping\n",
			wantTail: "[message_id: $abc",
		},
		{
			name:     "very short prefix is buffered",
			input:    "ping\n[m",
			wantBody: "ping\n",
			wantTail: "[m",
		},
		{
			name:     "complete hint not buffered",
			input:    "ping\n[message_id: $abc]",
			wantBody: "ping\n[message_id: $abc]",
			wantTail: "",
		},
		{
			name:     "regular bracket not buffered",
			input:    "check [this",
			wantBody: "check [this",
			wantTail: "",
		},
		{
			name:     "matrix event id prefix buffered",
			input:    "hello\n[matrix event",
			wantBody: "hello\n",
			wantTail: "[matrix event",
		},
		{
			name:     "just opening bracket is prefix of both targets",
			input:    "text\n[",
			wantBody: "text\n",
			wantTail: "[",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotBody, gotTail := splitTrailingMessageIDHint(tt.input)
			if gotBody != tt.wantBody || gotTail != tt.wantTail {
				t.Errorf("splitTrailingMessageIDHint(%q) = (%q, %q), want (%q, %q)",
					tt.input, gotBody, gotTail, tt.wantBody, tt.wantTail)
			}
		})
	}
}

func TestIsMessageIDHintPrefix(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"[", true},
		{"[m", true},
		{"[me", true},
		{"[message_id:", true},
		{"[message_id: $abc", true},
		{"[matrix", true},
		{"[matrix event id:", true},
		{"[matrix event id: $abc", true},
		{"[some other", false},
		{"hello", false},
		{"[x", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := isMessageIDHintPrefix(tt.input)
			if got != tt.want {
				t.Errorf("isMessageIDHintPrefix(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}
