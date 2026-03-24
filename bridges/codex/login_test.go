package codex

import (
	"context"
	"errors"
	"testing"
	"time"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote/bridges/codex/codexrpc"
)

func TestCodexLoginStartRejectsInvalidFlow(t *testing.T) {
	login := &CodexLogin{
		FlowID:    "invalid",
		Connector: &CodexConnector{Config: Config{Codex: &CodexConfig{Command: "zsh"}}},
	}
	_, err := login.Start(context.Background())
	if !errors.Is(err, bridgev2.ErrInvalidLoginFlowID) {
		t.Fatalf("expected invalid login flow error, got %v", err)
	}
}

func TestCodexLoginSubmitUserInputRequiresAPIKey(t *testing.T) {
	login := &CodexLogin{
		FlowID:    FlowCodexAPIKey,
		Connector: &CodexConnector{Config: Config{Codex: &CodexConfig{Command: "zsh"}}},
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
		Connector: &CodexConnector{Config: Config{Codex: &CodexConfig{Command: "zsh"}}},
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
	login := &CodexLogin{
		rpc:         &codexrpc.Client{},
		loginDoneCh: make(chan codexLoginDone),
		waitUntil:   time.Now().Add(-time.Second),
	}
	_, err := login.Wait(context.Background())
	var respErr bridgev2.RespError
	if !errors.As(err, &respErr) {
		t.Fatalf("expected RespError, got %T", err)
	}
	if respErr.ErrCode != "COM.BEEPER.AGENTREMOTE.CODEX.LOGIN_TIMEOUT" {
		t.Fatalf("unexpected errcode: %q", respErr.ErrCode)
	}
}
