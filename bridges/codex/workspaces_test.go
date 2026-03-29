package codex

import "testing"

func TestWorkspaceContains(t *testing.T) {
	if !workspaceContains("/repo", "/repo") {
		t.Fatal("expected root to contain itself")
	}
	if !workspaceContains("/repo", "/repo/a") {
		t.Fatal("expected /repo to contain /repo/a")
	}
	if !workspaceContains("/repo", "/repo/a/b") {
		t.Fatal("expected /repo to contain /repo/a/b")
	}
	if workspaceContains("/repo", "/repo2") {
		t.Fatal("expected /repo to not contain /repo2")
	}
}

func TestLongestMatchingWorkspaceRoot(t *testing.T) {
	roots := []string{"/repo", "/repo/a", "/repo/a/b"}
	got := longestMatchingWorkspaceRoot(roots, "/repo/a/b/c")
	if got != "/repo/a/b" {
		t.Fatalf("expected longest matching root /repo/a/b, got %q", got)
	}
}
