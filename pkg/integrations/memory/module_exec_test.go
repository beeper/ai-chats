package memory

import (
	"context"
	"fmt"
	"strings"
	"testing"

	iruntime "github.com/beeper/agentremote/pkg/integrations/runtime"
)

type mockManager struct {
	status        ProviderStatus
	statusDetails *MemorySearchStatus
}

func (m mockManager) Status() ProviderStatus {
	return m.status
}

func (m mockManager) Search(context.Context, string, SearchOptions) ([]SearchResult, error) {
	return nil, nil
}

func (m mockManager) ReadFile(context.Context, string, *int, *int) (map[string]any, error) {
	return nil, nil
}

func (m mockManager) StatusDetails(context.Context) (*MemorySearchStatus, error) {
	return m.statusDetails, nil
}

func (m mockManager) SyncWithProgress(context.Context, func(int, int, string)) error {
	return nil
}

func TestFormatStatusLines_LexicalModeOutput(t *testing.T) {
	lines := formatStatusLines(&MemorySearchStatus{
		Provider:     "builtin",
		Model:        "lexical",
		WorkspaceDir: "/workspace",
		DBPath:       "memory.db",
		Sources:      []string{"memory", "workspace"},
		ExtraPaths:   []string{"docs"},
		Files:        3,
		Chunks:       7,
		SourceCounts: []MemorySearchSourceCount{{Source: "memory", Files: 2, Chunks: 5}},
		FTS: &MemorySearchFTSStatus{
			Enabled:   true,
			Available: true,
		},
		Cache: &MemorySearchCacheStatus{
			Enabled:    true,
			Entries:    4,
			MaxEntries: 100,
		},
		Fallback: &FallbackStatus{
			From:   "openai",
			Reason: "rate_limit",
		},
	})

	output := strings.Join(lines, "\n")
	for _, needle := range []string{
		"Provider: builtin",
		"Model: lexical",
		"Workspace: /workspace",
		"DB: memory.db",
		"Sources: memory, workspace",
		"Extra paths: docs",
		"Files: 3",
		"Chunks: 7",
		"Source memory: 2 files / 5 chunks",
		"FTS enabled: true (available=true)",
		"Cache enabled: true (entries=4 max=100)",
		"Fallback: openai (rate_limit)",
	} {
		if !strings.Contains(output, needle) {
			t.Fatalf("expected status output to contain %q, got:\n%s", needle, output)
		}
	}

	for _, needle := range []string{
		"Requested provider:",
		"Vector enabled:",
		"Vector probe:",
		"Embedding probe:",
		"Batch enabled:",
	} {
		if strings.Contains(output, needle) {
			t.Fatalf("did not expect status output to contain %q, got:\n%s", needle, output)
		}
	}
}

func TestExecuteCommand_StatusDeepAliasUsesLexicalStatusOutput(t *testing.T) {
	manager := mockManager{
		statusDetails: &MemorySearchStatus{
			Provider:     "builtin",
			Model:        "lexical",
			WorkspaceDir: "/workspace",
			DBPath:       "memory.db",
			Sources:      []string{"memory"},
			FTS: &MemorySearchFTSStatus{
				Enabled:   true,
				Available: true,
			},
			Cache: &MemorySearchCacheStatus{
				Enabled:    true,
				Entries:    2,
				MaxEntries: 50,
			},
		},
	}

	var replies []string
	call := iruntime.CommandCall{
		Name: "memory",
		Args: []string{"status", "deep"},
		Reply: func(format string, args ...any) {
			replies = append(replies, fmt.Sprintf(format, args...))
		},
	}

	handled, err := ExecuteCommand(context.Background(), call, CommandExecDeps{
		GetManager: func(iruntime.ToolScope) (execManager, string) {
			return manager, ""
		},
	})
	if err != nil {
		t.Fatalf("ExecuteCommand returned error: %v", err)
	}
	if !handled {
		t.Fatalf("expected command to be handled")
	}
	if len(replies) != 1 {
		t.Fatalf("expected one reply, got %d", len(replies))
	}

	output := replies[0]
	for _, needle := range []string{
		"Provider: builtin",
		"Model: lexical",
		"FTS enabled: true (available=true)",
		"Cache enabled: true (entries=2 max=50)",
	} {
		if !strings.Contains(output, needle) {
			t.Fatalf("expected command output to contain %q, got:\n%s", needle, output)
		}
	}
	for _, needle := range []string{
		"Requested provider:",
		"Vector enabled:",
		"Vector probe:",
		"Embedding probe:",
		"Batch enabled:",
	} {
		if strings.Contains(output, needle) {
			t.Fatalf("did not expect command output to contain %q, got:\n%s", needle, output)
		}
	}
}

func TestFormatStatusLines_UnlimitedCacheOutput(t *testing.T) {
	lines := formatStatusLines(&MemorySearchStatus{
		Cache: &MemorySearchCacheStatus{
			Enabled:    true,
			Entries:    4,
			MaxEntries: UnlimitedCacheEntries,
		},
	})

	output := strings.Join(lines, "\n")
	if !strings.Contains(output, "Cache enabled: true (entries=4 max=unlimited)") {
		t.Fatalf("expected unlimited cache output, got:\n%s", output)
	}
}

func TestNormalizeCitationsMode(t *testing.T) {
	if got := normalizeCitationsMode(""); got != "auto" {
		t.Fatalf("expected empty citations mode to normalize to auto, got %q", got)
	}
	if got := normalizeCitationsMode("ON"); got != "on" {
		t.Fatalf("expected ON to normalize to on, got %q", got)
	}
	if got := normalizeCitationsMode("weird"); got != "auto" {
		t.Fatalf("expected unknown citations mode to normalize to auto, got %q", got)
	}
}
