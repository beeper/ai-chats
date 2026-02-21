package connector

import (
	"testing"

	"github.com/beeper/ai-bridge/pkg/agents"
)

func TestMergeMemorySearchConfig_DefaultsEnabled(t *testing.T) {
	cfg := mergeMemorySearchConfig(nil, nil)
	if cfg == nil {
		t.Fatal("expected memory search to be enabled by default")
	}
	if !cfg.Enabled {
		t.Fatal("expected memory search Enabled=true by default")
	}
	if cfg.Provider != "auto" {
		t.Fatalf("expected provider auto, got %q", cfg.Provider)
	}
}

func TestMergeMemorySearchConfig_StoreForcedToSQLiteVFS(t *testing.T) {
	defaults := &MemorySearchConfig{
		Store: &MemorySearchStoreConfig{
			Driver: "duckdb",
			Path:   "/tmp/custom.db",
		},
	}
	overrides := &agents.MemorySearchConfig{
		Store: &agents.MemorySearchStoreConfig{
			Driver: "postgres",
			Path:   "/var/lib/other.db",
		},
	}

	cfg := mergeMemorySearchConfig(defaults, overrides)
	if cfg == nil {
		t.Fatal("expected memory search config")
	}
	if cfg.Store.Driver != "sqlite" {
		t.Fatalf("expected sqlite driver, got %q", cfg.Store.Driver)
	}
	if cfg.Store.Path != "" {
		t.Fatalf("expected empty store path for VFS-only mode, got %q", cfg.Store.Path)
	}
}
