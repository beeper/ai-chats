package opencode

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"maunium.net/go/mautrix/bridgev2"
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

func TestOpenCodeLoginStartRejectsInvalidFlow(t *testing.T) {
	login := &OpenCodeLogin{
		User:      &bridgev2.User{},
		Connector: &OpenCodeConnector{br: &bridgev2.Bridge{}},
		FlowID:    "invalid",
	}
	_, err := login.Start(context.Background())
	if !errors.Is(err, bridgev2.ErrInvalidLoginFlowID) {
		t.Fatalf("expected invalid login flow error, got %v", err)
	}
}

func assertOpenCodeRespError(t *testing.T, err error, status int, code string) {
	t.Helper()

	var respErr bridgev2.RespError
	if !errors.As(err, &respErr) {
		t.Fatalf("expected RespError, got %T", err)
	}
	if respErr.StatusCode != status {
		t.Fatalf("unexpected status code: %d", respErr.StatusCode)
	}
	if respErr.ErrCode != code {
		t.Fatalf("unexpected errcode: %q", respErr.ErrCode)
	}
}

func TestOpenCodeLoginValidationErrorMappings(t *testing.T) {
	login := &OpenCodeLogin{}

	tests := []struct {
		name       string
		run        func(t *testing.T) error
		wantStatus int
		wantCode   string
	}{
		{
			name: "invalid URL",
			run: func(t *testing.T) error {
				t.Helper()
				_, _, _, err := login.buildRemoteInstances(map[string]string{"url": "://bad-url"})
				return err
			},
			wantStatus: http.StatusBadRequest,
			wantCode:   "COM.BEEPER.AGENTREMOTE.OPENCODE.INVALID_URL",
		},
		{
			name: "invalid binary path",
			run: func(t *testing.T) error {
				t.Helper()
				_, err := resolveManagedOpenCodeBinary(filepath.Join(t.TempDir(), "missing-opencode"))
				return err
			},
			wantStatus: http.StatusBadRequest,
			wantCode:   "COM.BEEPER.AGENTREMOTE.OPENCODE.INVALID_BINARY_PATH",
		},
		{
			name: "missing default path",
			run: func(t *testing.T) error {
				t.Helper()
				orig := defaultManagedOpenCodeDirectoryFn
				defaultManagedOpenCodeDirectoryFn = func() string { return "" }
				t.Cleanup(func() {
					defaultManagedOpenCodeDirectoryFn = orig
				})
				_, err := resolveManagedOpenCodeDirectory("")
				return err
			},
			wantStatus: http.StatusBadRequest,
			wantCode:   "COM.BEEPER.AGENTREMOTE.OPENCODE.DEFAULT_PATH_REQUIRED",
		},
		{
			name: "inaccessible default path",
			run: func(t *testing.T) error {
				t.Helper()
				_, err := resolveManagedOpenCodeDirectory(filepath.Join(t.TempDir(), "missing"))
				return err
			},
			wantStatus: http.StatusBadRequest,
			wantCode:   "COM.BEEPER.AGENTREMOTE.OPENCODE.DEFAULT_PATH_NOT_ACCESSIBLE",
		},
		{
			name: "default path not directory",
			run: func(t *testing.T) error {
				t.Helper()
				filePath := filepath.Join(t.TempDir(), "not-a-dir")
				if err := os.WriteFile(filePath, []byte("x"), 0o644); err != nil {
					t.Fatalf("failed to create file: %v", err)
				}
				_, err := resolveManagedOpenCodeDirectory(filePath)
				return err
			},
			wantStatus: http.StatusBadRequest,
			wantCode:   "COM.BEEPER.AGENTREMOTE.OPENCODE.DEFAULT_PATH_NOT_DIRECTORY",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertOpenCodeRespError(t, tc.run(t), tc.wantStatus, tc.wantCode)
		})
	}
}
