package codex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote/bridges/codex/codexrpc"
)

func TestCodexLoginWaitTimeoutHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_CODEX_TIMEOUT_HELPER") != "1" {
		return
	}
	select {}
}

func testCodexCommand(t *testing.T) string {
	t.Helper()

	cmd, err := os.Executable()
	if err != nil {
		t.Fatalf("failed to resolve test executable: %v", err)
	}
	return cmd
}

func TestCodexLoginStartRejectsInvalidFlow(t *testing.T) {
	login := &CodexLogin{
		FlowID:    "invalid",
		Connector: &CodexConnector{Config: Config{Codex: &CodexConfig{Command: testCodexCommand(t)}}},
	}
	_, err := login.Start(context.Background())
	if !errors.Is(err, bridgev2.ErrInvalidLoginFlowID) {
		t.Fatalf("expected invalid login flow error, got %v", err)
	}
}

func TestCodexLoginSubmitUserInputRequiresAPIKey(t *testing.T) {
	login := &CodexLogin{
		FlowID:    FlowCodexAPIKey,
		Connector: &CodexConnector{Config: Config{Codex: &CodexConfig{Command: testCodexCommand(t)}}},
	}
	_, err := login.SubmitUserInput(context.Background(), map[string]string{})
	var respErr bridgev2.RespError
	if !errors.As(err, &respErr) {
		t.Fatalf("expected RespError, got %T", err)
	}
	if respErr.ErrCode != "COM.BEEPER.AGENTREMOTE.CODEX.API_KEY_REQUIRED" {
		t.Fatalf("unexpected errcode: %q", respErr.ErrCode)
	}
}

func TestCodexLoginSubmitUserInputRequiresExternalTokens(t *testing.T) {
	login := &CodexLogin{
		FlowID:    FlowCodexChatGPTExternalTokens,
		Connector: &CodexConnector{Config: Config{Codex: &CodexConfig{Command: testCodexCommand(t)}}},
	}
	_, err := login.SubmitUserInput(context.Background(), map[string]string{"access_token": "token"})
	var respErr bridgev2.RespError
	if !errors.As(err, &respErr) {
		t.Fatalf("expected RespError, got %T", err)
	}
	if respErr.ErrCode != "COM.BEEPER.AGENTREMOTE.CODEX.CHATGPT_TOKENS_REQUIRED" {
		t.Fatalf("unexpected errcode: %q", respErr.ErrCode)
	}
}

func TestCodexLoginWaitRequiresStart(t *testing.T) {
	login := &CodexLogin{}
	_, err := login.Wait(context.Background())
	var respErr bridgev2.RespError
	if !errors.As(err, &respErr) {
		t.Fatalf("expected RespError, got %T", err)
	}
	if respErr.ErrCode != "COM.BEEPER.AGENTREMOTE.CODEX.NOT_STARTED" {
		t.Fatalf("unexpected errcode: %q", respErr.ErrCode)
	}
}

func TestCodexLoginWaitTimeoutReturnsTypedError(t *testing.T) {
	codexHome := filepath.Join(t.TempDir(), "codex-home")
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		t.Fatalf("failed to create codex home: %v", err)
	}

	rpc, err := codexrpc.StartProcess(context.Background(), codexrpc.ProcessConfig{
		Command: testCodexCommand(t),
		Args:    []string{"-test.run=TestCodexLoginWaitTimeoutHelperProcess"},
		Env:     []string{"GO_WANT_CODEX_TIMEOUT_HELPER=1"},
	})
	if err != nil {
		t.Fatalf("failed to start helper codex rpc process: %v", err)
	}

	cancelled := false
	login := &CodexLogin{
		rpc:         rpc,
		cancel:      func() { cancelled = true },
		codexHome:   codexHome,
		loginDoneCh: make(chan codexLoginDone),
		waitUntil:   time.Now().Add(-time.Second),
	}
	_, err = login.Wait(context.Background())
	var respErr bridgev2.RespError
	if !errors.As(err, &respErr) {
		t.Fatalf("expected RespError, got %T", err)
	}
	if respErr.ErrCode != "COM.BEEPER.AGENTREMOTE.CODEX.LOGIN_TIMEOUT" {
		t.Fatalf("unexpected errcode: %q", respErr.ErrCode)
	}
	if !cancelled {
		t.Fatal("expected timed out wait to cancel the pending login")
	}
	if _, err := os.Stat(codexHome); !os.IsNotExist(err) {
		t.Fatalf("expected codex home to be removed, stat error = %v", err)
	}
}
