package codex

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveCodexWorkingDirectoryExpandsTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cc := &CodexClient{}

	got, err := cc.resolveManagedPathArgument("~/workspace/project", nil)
	if err != nil {
		t.Fatalf("resolveManagedPathArgument returned error: %v", err)
	}

	want := filepath.Join(home, "workspace", "project")
	if got != want {
		t.Fatalf("resolveManagedPathArgument returned %q, want %q", got, want)
	}
}

func TestResolveCodexWorkingDirectoryExpandsBareTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cc := &CodexClient{}

	got, err := cc.resolveManagedPathArgument("~", nil)
	if err != nil {
		t.Fatalf("resolveManagedPathArgument returned error: %v", err)
	}
	if got != home {
		t.Fatalf("resolveManagedPathArgument returned %q, want %q", got, home)
	}
}

func TestResolveCodexWorkingDirectoryAcceptsAbsolutePath(t *testing.T) {
	want := filepath.Join(string(filepath.Separator), "tmp", "workspace")
	cc := &CodexClient{}

	got, err := cc.resolveManagedPathArgument(want, nil)
	if err != nil {
		t.Fatalf("resolveManagedPathArgument returned error: %v", err)
	}
	if got != want {
		t.Fatalf("resolveManagedPathArgument returned %q, want %q", got, want)
	}
}

func TestResolveCodexWorkingDirectoryRejectsRelativePath(t *testing.T) {
	cc := &CodexClient{}
	if _, err := cc.resolveManagedPathArgument("projects/labs", nil); err == nil {
		t.Fatal("expected relative path to be rejected")
	}
}

func TestIsManagedCodexTempDirPath(t *testing.T) {
	cases := []struct {
		name string
		path string
		want bool
	}{
		{
			name: "mkdtemp path",
			path: filepath.Join(os.TempDir(), "agentremote-codex-12345"),
			want: true,
		},
		{
			name: "fallback temp root",
			path: filepath.Join(os.TempDir(), "agentremote-codex", "instance-12345"),
			want: true,
		},
		{
			name: "unmanaged path",
			path: filepath.Join(os.TempDir(), "workspace", "instance-12345"),
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isManagedCodexTempDirPath(tc.path); got != tc.want {
				t.Fatalf("isManagedCodexTempDirPath(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}
