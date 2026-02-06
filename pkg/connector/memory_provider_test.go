package connector

import (
	"strings"
	"testing"

	"github.com/beeper/ai-bridge/pkg/memory"
)

func TestBuildMemoryProviderRejectsLocalProvider(t *testing.T) {
	cfg := &memory.ResolvedConfig{
		Provider: "local",
		Fallback: "none",
	}

	_, err := buildMemoryProvider(&AIClient{}, cfg)
	if err == nil {
		t.Fatal("expected local provider to be rejected")
	}
	if !strings.Contains(err.Error(), "unsupported embeddings provider: local") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuildMemoryProviderAutoRequiresRemoteProvider(t *testing.T) {
	cfg := &memory.ResolvedConfig{
		Provider: "auto",
		Fallback: "none",
	}

	_, err := buildMemoryProvider(&AIClient{}, cfg)
	if err == nil {
		t.Fatal("expected no-provider error")
	}
	if !strings.Contains(err.Error(), "no embeddings provider available") {
		t.Fatalf("unexpected error: %v", err)
	}
}
