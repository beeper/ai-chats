package opencode

import (
	"testing"

	"github.com/beeper/agentremote/bridges/opencode/api"
)

func TestBuildOpenCodeApprovalPresentation(t *testing.T) {
	p := buildOpenCodeApprovalPresentation(api.PermissionRequest{
		Permission: "filesystem.write",
		Patterns:   []string{"src/**", "pkg/**"},
		Always:     []string{"workspace"},
		Metadata: map[string]any{
			"cwd": "/repo",
		},
	})
	if p.Title == "" {
		t.Fatalf("expected title")
	}
	if !p.AllowAlways {
		t.Fatalf("expected OpenCode approvals to allow always")
	}
	if len(p.Details) == 0 {
		t.Fatalf("expected details")
	}
}
