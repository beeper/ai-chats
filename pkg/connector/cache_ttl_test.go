package connector

import (
	"testing"
	"time"
)

func TestIsCacheTTLEligibleProvider(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		{"anthropic/claude-opus-4.5", true},
		{"anthropic/claude-sonnet-4.5", true},
		{"claude-haiku-4.5", true},
		{"openai/gpt-5", false},
		{"google/gemini-3-pro", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := IsCacheTTLEligibleProvider(tt.model); got != tt.want {
			t.Errorf("IsCacheTTLEligibleProvider(%q) = %v, want %v", tt.model, got, tt.want)
		}
	}
}

func TestShouldRefreshCacheTTL_NilMeta(t *testing.T) {
	if !ShouldRefreshCacheTTL(nil) {
		t.Fatal("expected true for nil metadata")
	}
}

func TestShouldRefreshCacheTTL_NeverRefreshed(t *testing.T) {
	meta := &PortalMetadata{}
	if !ShouldRefreshCacheTTL(meta) {
		t.Fatal("expected true when LastCacheTTLRefresh is 0")
	}
}

func TestShouldRefreshCacheTTL_RecentRefresh(t *testing.T) {
	meta := &PortalMetadata{
		LastCacheTTLRefresh: time.Now().UnixMilli(),
	}
	if ShouldRefreshCacheTTL(meta) {
		t.Fatal("expected false for recent refresh")
	}
}

func TestShouldRefreshCacheTTL_ExpiredRefresh(t *testing.T) {
	meta := &PortalMetadata{
		LastCacheTTLRefresh: time.Now().Add(-6 * time.Minute).UnixMilli(),
	}
	if !ShouldRefreshCacheTTL(meta) {
		t.Fatal("expected true for expired refresh")
	}
}

func TestAppendCacheTTLTimestamp(t *testing.T) {
	meta := &PortalMetadata{}
	AppendCacheTTLTimestamp(meta)
	if meta.LastCacheTTLRefresh == 0 {
		t.Fatal("expected non-zero timestamp after append")
	}
}

func TestAppendCacheTTLTimestamp_NilMeta(t *testing.T) {
	// Should not panic
	AppendCacheTTLTimestamp(nil)
}
