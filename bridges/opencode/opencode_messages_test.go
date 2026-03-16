package opencode

import (
	"path/filepath"
	"testing"

	"github.com/beeper/agentremote/bridges/opencode/api"
)

func TestOpenCodeSessionUsesDirectory(t *testing.T) {
	t.Run("matches exact path", func(t *testing.T) {
		if !openCodeSessionUsesDirectory("/tmp/work", &api.Session{Directory: "/tmp/work"}) {
			t.Fatal("expected directory match")
		}
	})

	t.Run("matches cleaned path", func(t *testing.T) {
		if !openCodeSessionUsesDirectory("/tmp/work/../work", &api.Session{Directory: "/tmp/work"}) {
			t.Fatal("expected cleaned directory match")
		}
	})

	t.Run("rejects mismatched path", func(t *testing.T) {
		if openCodeSessionUsesDirectory("/tmp/work", &api.Session{Directory: "/tmp/else"}) {
			t.Fatal("expected mismatched directory to be rejected")
		}
	})

	t.Run("rejects missing reported directory", func(t *testing.T) {
		if openCodeSessionUsesDirectory("/tmp/work", &api.Session{}) {
			t.Fatal("expected missing directory to be rejected")
		}
	})
}

func TestResolveManagedWorkingDirectory(t *testing.T) {
	t.Run("uses explicit absolute path", func(t *testing.T) {
		got, err := resolveManagedWorkingDirectory("/tmp/work", "/tmp/default")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "/tmp/work" {
			t.Fatalf("expected explicit path, got %q", got)
		}
	})

	t.Run("falls back to default path", func(t *testing.T) {
		got, err := resolveManagedWorkingDirectory("", "/tmp/default")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "/tmp/default" {
			t.Fatalf("expected default path, got %q", got)
		}
	})

	t.Run("expands tilde path", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)

		got, err := resolveManagedWorkingDirectory("~/worktree", "/tmp/default")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := filepath.Join(home, "worktree")
		if got != want {
			t.Fatalf("expected expanded path %q, got %q", want, got)
		}
	})

	t.Run("expands bare tilde", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)

		got, err := resolveManagedWorkingDirectory("~", "/tmp/default")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != home {
			t.Fatalf("expected expanded home %q, got %q", home, got)
		}
	})

	t.Run("rejects relative path", func(t *testing.T) {
		if _, err := resolveManagedWorkingDirectory("relative/path", "/tmp/default"); err == nil {
			t.Fatal("expected relative path to be rejected")
		}
	})
}
