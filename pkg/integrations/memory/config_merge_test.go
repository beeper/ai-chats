package memory

import (
	"testing"

	"go.mau.fi/util/ptr"

	"github.com/beeper/agentremote/pkg/agents"
)

func TestMergeSearchConfig_NormalizesUnlimitedCacheEntries(t *testing.T) {
	cfg := MergeSearchConfig(&agents.MemorySearchConfig{
		Cache: &agents.MemorySearchCacheConfig{
			Enabled:    ptr.Ptr(true),
			MaxEntries: 0,
		},
	}, nil)
	if cfg == nil {
		t.Fatal("expected resolved config")
	}
	if cfg.Cache.MaxEntries != UnlimitedCacheEntries {
		t.Fatalf("expected cache max entries %d, got %d", UnlimitedCacheEntries, cfg.Cache.MaxEntries)
	}

	cfg = MergeSearchConfig(&agents.MemorySearchConfig{
		Cache: &agents.MemorySearchCacheConfig{
			Enabled:    ptr.Ptr(true),
			MaxEntries: -25,
		},
	}, nil)
	if cfg == nil {
		t.Fatal("expected resolved config")
	}
	if cfg.Cache.MaxEntries != UnlimitedCacheEntries {
		t.Fatalf("expected negative cache max entries to normalize to %d, got %d", UnlimitedCacheEntries, cfg.Cache.MaxEntries)
	}

	cfg = MergeSearchConfig(&agents.MemorySearchConfig{
		Cache: &agents.MemorySearchCacheConfig{
			Enabled:    ptr.Ptr(true),
			MaxEntries: 12,
		},
	}, nil)
	if cfg == nil {
		t.Fatal("expected resolved config")
	}
	if cfg.Cache.MaxEntries != 12 {
		t.Fatalf("expected positive cache max entries to stay unchanged, got %d", cfg.Cache.MaxEntries)
	}
}
