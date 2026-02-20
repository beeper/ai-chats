package connector

import (
	"testing"

	"github.com/beeper/ai-bridge/pkg/memory"
)

func TestResolveMemoryDBPathUsesBridgeSQLiteVFS(t *testing.T) {
	cfg := &memory.ResolvedConfig{
		Store: memory.StoreConfig{
			Driver: "sqlite",
			Path:   "/tmp/legacy-memory.db",
		},
	}
	if got := resolveMemoryDBPath(cfg, "beeper"); got != "bridge.sqlite (vfs)" {
		t.Fatalf("expected VFS db path marker, got %q", got)
	}
}
