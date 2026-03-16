package ai

import "testing"

func TestBuildBuiltinApprovalPresentation(t *testing.T) {
	presentation := buildBuiltinApprovalPresentation("commandExecution", "run", map[string]any{
		"command": "ls -la",
		"cwd":     "/tmp",
	})
	if !presentation.AllowAlways {
		t.Fatalf("expected builtin approvals to allow always")
	}
	if presentation.Title == "" {
		t.Fatalf("expected title")
	}
	if len(presentation.Details) == 0 {
		t.Fatalf("expected details")
	}
}

func TestBuildMCPApprovalPresentation(t *testing.T) {
	presentation := buildMCPApprovalPresentation("filesystem", "read_file", map[string]any{
		"path": "/tmp/demo.txt",
	})
	if !presentation.AllowAlways {
		t.Fatalf("expected MCP approvals to allow always")
	}
	if presentation.Title == "" {
		t.Fatalf("expected title")
	}
	if len(presentation.Details) == 0 {
		t.Fatalf("expected details")
	}
}

func TestBuildBuiltinApprovalPresentation_EdgeCases(t *testing.T) {
	testCases := []struct {
		name string
		args map[string]any
	}{
		{name: "nil args", args: nil},
		{name: "empty args", args: map[string]any{}},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			presentation := buildBuiltinApprovalPresentation("", "", tc.args)
			if presentation.Title == "" {
				t.Fatal("expected fallback title")
			}
			if !presentation.AllowAlways {
				t.Fatal("expected allow-always to remain enabled")
			}
		})
	}
}

func TestBuildMCPApprovalPresentation_EdgeCases(t *testing.T) {
	testCases := []struct {
		name  string
		input any
	}{
		{name: "nil input", input: nil},
		{name: "empty map", input: map[string]any{}},
		{name: "non map input", input: []string{"value"}},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			presentation := buildMCPApprovalPresentation("", "", tc.input)
			if presentation.Title == "" {
				t.Fatal("expected fallback title")
			}
			if !presentation.AllowAlways {
				t.Fatal("expected allow-always to remain enabled")
			}
		})
	}
}
