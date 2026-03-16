package opencode

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGetLoginFlowsIncludesRemoteAndManaged(t *testing.T) {
	connector := NewConnector()
	flows := connector.GetLoginFlows()
	if len(flows) != 2 {
		t.Fatalf("expected 2 login flows, got %d", len(flows))
	}
	if flows[0].ID != FlowOpenCodeRemote {
		t.Fatalf("expected first flow to be remote, got %q", flows[0].ID)
	}
	if flows[1].ID != FlowOpenCodeManaged {
		t.Fatalf("expected second flow to be managed, got %q", flows[1].ID)
	}
}

func TestResolveManagedOpenCodeDirectoryExpandsTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	target := filepath.Join(home, "workspace")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatalf("failed to create target directory: %v", err)
	}

	got, err := resolveManagedOpenCodeDirectory("~/workspace")
	if err != nil {
		t.Fatalf("resolveManagedOpenCodeDirectory returned error: %v", err)
	}
	if got != target {
		t.Fatalf("resolveManagedOpenCodeDirectory returned %q, want %q", got, target)
	}
}

func TestResolveManagedOpenCodeDirectoryExpandsBareTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got, err := resolveManagedOpenCodeDirectory("~")
	if err != nil {
		t.Fatalf("resolveManagedOpenCodeDirectory returned error: %v", err)
	}
	if got != home {
		t.Fatalf("resolveManagedOpenCodeDirectory returned %q, want %q", got, home)
	}
}
