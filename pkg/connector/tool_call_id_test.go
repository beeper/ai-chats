package connector

import (
	"strings"
	"testing"
)

func TestSanitizeToolCallID_Strict(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"call_abc123", "call_abc123"},
		{"call_abc-123_xyz", "call_abc123xyz"},
		{"call_abc!@#def", "call_abcdef"},
		{"abc123", "abc123"},
		{"abc-def.ghi", "abcdefghi"},
	}
	for _, tt := range tests {
		got := SanitizeToolCallID(tt.input, "strict")
		if got != tt.want {
			t.Errorf("SanitizeToolCallID(%q, strict) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestSanitizeToolCallID_Strict9(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"call_abcdefghijklmno", "call_abcdefghi"},
		{"call_abc", "call_abc"},
		{"abcdefghijklmno", "abcdefghi"},
	}
	for _, tt := range tests {
		got := SanitizeToolCallID(tt.input, "strict9")
		if got != tt.want {
			t.Errorf("SanitizeToolCallID(%q, strict9) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestSanitizeToolCallID_EmptyInput(t *testing.T) {
	got := SanitizeToolCallID("", "strict")
	if !strings.HasPrefix(got, "call_") {
		t.Errorf("expected generated call ID with call_ prefix, got %q", got)
	}
}

func TestSanitizeToolCallID_AllSpecialChars(t *testing.T) {
	got := SanitizeToolCallID("!@#$%^&*()", "strict")
	if !strings.HasPrefix(got, "call_") {
		t.Errorf("expected generated call ID for all-special input, got %q", got)
	}
}

func TestSanitizeToolCallID_CallPrefixAllSpecial(t *testing.T) {
	got := SanitizeToolCallID("call_!@#$", "strict")
	if !strings.HasPrefix(got, "call_") {
		t.Errorf("expected generated call ID, got %q", got)
	}
}
