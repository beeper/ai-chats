package openclaw

import "testing"

func TestOpenClawApprovalPresentation(t *testing.T) {
	p := openClawApprovalPresentation(map[string]any{
		"command":    "rm -rf /tmp/x",
		"cwd":        "/tmp",
		"reason":     "cleanup",
		"sessionKey": "sess-1",
	}, "rm -rf /tmp/x")
	if p.Title == "" {
		t.Fatalf("expected title")
	}
	if !p.AllowAlways {
		t.Fatalf("expected OpenClaw approvals to allow always")
	}
	if len(p.Details) == 0 {
		t.Fatalf("expected details")
	}
}
