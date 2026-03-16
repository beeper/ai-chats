package codex

import "testing"

func TestBuildSandboxMode(t *testing.T) {
	cc := &CodexClient{}
	if got := cc.buildSandboxMode(); got != "workspace-write" {
		t.Fatalf("buildSandboxMode() = %q, want %q", got, "workspace-write")
	}
}

func TestBuildSandboxPolicy(t *testing.T) {
	cc := &CodexClient{}
	cwd := "/tmp/workspace"

	got := cc.buildSandboxPolicy(cwd)

	if got["type"] != "workspaceWrite" {
		t.Fatalf("policy type = %#v, want %q", got["type"], "workspaceWrite")
	}
	if got["networkAccess"] != true {
		t.Fatalf("networkAccess = %#v, want true", got["networkAccess"])
	}
	if got["excludeTmpdirEnvVar"] != false {
		t.Fatalf("excludeTmpdirEnvVar = %#v, want false", got["excludeTmpdirEnvVar"])
	}
	if got["excludeSlashTmp"] != false {
		t.Fatalf("excludeSlashTmp = %#v, want false", got["excludeSlashTmp"])
	}

	roots, ok := got["writableRoots"].([]string)
	if !ok {
		t.Fatalf("writableRoots type = %T, want []string", got["writableRoots"])
	}
	if len(roots) != 1 || roots[0] != cwd {
		t.Fatalf("writableRoots = %#v, want [%q]", roots, cwd)
	}

	access, ok := got["readOnlyAccess"].(map[string]any)
	if !ok {
		t.Fatalf("readOnlyAccess type = %T, want map[string]any", got["readOnlyAccess"])
	}
	if access["type"] != "fullAccess" {
		t.Fatalf("readOnlyAccess.type = %#v, want %q", access["type"], "fullAccess")
	}
}
