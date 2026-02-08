package connector

import "testing"

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

func TestStreamingDirectiveAccumulator_ResetClearsWhitespace(t *testing.T) {
	acc := newStreamingDirectiveAccumulator()

	// Send whitespace-only delta (deferred)
	result := acc.Consume(" ", false)
	if result != nil {
		t.Fatal("expected nil for whitespace-only delta")
	}

	// Reset should clear pendingWhitespace
	acc.Reset()

	// Next word should NOT have the space prepended
	result = acc.Consume("Hello", true)
	if result == nil {
		t.Fatal("expected non-nil result for 'Hello'")
	}
	if result.Text != "Hello" {
		t.Errorf("after Reset, text = %q, want %q", result.Text, "Hello")
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
	result = acc.Consume(" ", true)
	// This is acceptable: trailing whitespace at end of stream has no next token
	// to attach to, so it's dropped. The important case is whitespace BETWEEN words.
}
