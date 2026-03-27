package main

import (
	"encoding/json"
	"io"
	"os"
	"sort"
	"testing"
)

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe returned error: %v", err)
	}
	os.Stdout = w
	defer func() {
		os.Stdout = oldStdout
	}()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("closing writer returned error: %v", err)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("reading stdout returned error: %v", err)
	}
	return string(data)
}

func TestCmdStatusUsesDeviceScopedBridgeNames(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	if err := saveProfileState(defaultProfile, &profileState{DeviceID: "abc123def0"}); err != nil {
		t.Fatalf("saveProfileState returned error: %v", err)
	}
	if _, err := ensureInstanceLayout(defaultProfile, "ai"); err != nil {
		t.Fatalf("ensureInstanceLayout ai returned error: %v", err)
	}
	if _, err := ensureInstanceLayout(defaultProfile, "codex-dev"); err != nil {
		t.Fatalf("ensureInstanceLayout codex-dev returned error: %v", err)
	}

	output := captureStdout(t, func() {
		if err := cmdStatus([]string{"--profile", defaultProfile, "--no-remote", "--output", "json"}); err != nil {
			t.Fatalf("cmdStatus returned error: %v", err)
		}
	})

	var statuses []bridgeStatus
	if err := json.Unmarshal([]byte(output), &statuses); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v\noutput=%s", err, output)
	}
	if len(statuses) != 2 {
		t.Fatalf("expected 2 statuses, got %d", len(statuses))
	}

	names := make([]string, 0, len(statuses))
	for _, status := range statuses {
		if status.Local == nil {
			t.Fatalf("expected local status for %q", status.Name)
		}
		names = append(names, status.Name)
	}
	sort.Strings(names)

	want := []string{"sh-abc123def0-ai", "sh-abc123def0-codex-dev"}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("unexpected status names: got %#v want %#v", names, want)
		}
	}
}
