package main

import (
	"os"
	"strings"
	"testing"

	codexbridge "github.com/beeper/agentremote/bridges/codex"
)

func writeCodexConfigForTest(t *testing.T, profile, name string, tracked []string) string {
	t.Helper()
	sp, err := ensureInstanceLayout(profile, instanceDirName("codex", name))
	if err != nil {
		t.Fatalf("ensureInstanceLayout returned error: %v", err)
	}
	content := "codex:\n  tracked_paths:\n"
	for _, path := range tracked {
		content += "    - " + path + "\n"
	}
	if err := os.WriteFile(sp.ConfigPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	return sp.ConfigPath
}

func TestCmdCodexAddUsesCWDByDefault(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	configPath := writeCodexConfigForTest(t, defaultProfile, "", nil)
	cwd := t.TempDir()
	oldWD, _ := os.Getwd()
	if err := os.Chdir(cwd); err != nil {
		t.Fatalf("Chdir returned error: %v", err)
	}
	defer func() { _ = os.Chdir(oldWD) }()
	currentWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd returned error: %v", err)
	}
	normalizedCWD, err := codexbridge.ResolveCodexWorkingDirectoryCLI(currentWD)
	if err != nil {
		t.Fatalf("ResolveCodexWorkingDirectoryCLI returned error: %v", err)
	}

	if err := cmdCodex([]string{"add", "--profile", defaultProfile}); err != nil {
		t.Fatalf("cmdCodex add returned error: %v", err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if !strings.Contains(string(data), normalizedCWD) {
		t.Fatalf("expected config to contain cwd %q, got:\n%s", normalizedCWD, data)
	}
}

func TestCmdCodexRemoveUsesCWDByDefault(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cwd := t.TempDir()
	oldWD, _ := os.Getwd()
	if err := os.Chdir(cwd); err != nil {
		t.Fatalf("Chdir returned error: %v", err)
	}
	defer func() { _ = os.Chdir(oldWD) }()
	currentWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd returned error: %v", err)
	}
	normalizedCWD, err := codexbridge.ResolveCodexWorkingDirectoryCLI(currentWD)
	if err != nil {
		t.Fatalf("ResolveCodexWorkingDirectoryCLI returned error: %v", err)
	}
	configPath := writeCodexConfigForTest(t, defaultProfile, "", []string{normalizedCWD})

	if err := cmdCodex([]string{"remove", "--profile", defaultProfile}); err != nil {
		t.Fatalf("cmdCodex remove returned error: %v", err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if strings.Contains(string(data), normalizedCWD) {
		t.Fatalf("expected config to remove cwd %q, got:\n%s", normalizedCWD, data)
	}
}

func TestCmdCodexDirsPrintsTrackedPaths(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeCodexConfigForTest(t, defaultProfile, "dev", []string{"/repo", "/repo/sub"})

	output := captureStdout(t, func() {
		if err := cmdCodex([]string{"dirs", "--profile", defaultProfile, "--name", "dev"}); err != nil {
			t.Fatalf("cmdCodex dirs returned error: %v", err)
		}
	})
	if !strings.Contains(output, "/repo") || !strings.Contains(output, "/repo/sub") {
		t.Fatalf("expected tracked paths in output, got %q", output)
	}
}
