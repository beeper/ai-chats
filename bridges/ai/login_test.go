package ai

import (
	"context"
	"errors"
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/id"
)

func TestOpenAILoginStartRejectsInvalidFlow(t *testing.T) {
	login := &OpenAILogin{FlowID: "invalid"}
	_, err := login.Start(context.Background())
	if !errors.Is(err, bridgev2.ErrInvalidLoginFlowID) {
		t.Fatalf("expected invalid login flow error, got %v", err)
	}
}

func TestOpenAILoginStartWithOverrideRejectsInvalidTarget(t *testing.T) {
	login := &OpenAILogin{User: &bridgev2.User{User: &database.User{MXID: id.UserID("@alice:example.com")}}}
	old := &bridgev2.UserLogin{UserLogin: &database.UserLogin{UserMXID: id.UserID("@bob:example.com")}}
	_, err := login.StartWithOverride(context.Background(), old)
	var respErr bridgev2.RespError
	if !errors.As(err, &respErr) {
		t.Fatalf("expected RespError, got %T", err)
	}
	if respErr.ErrCode != "COM.BEEPER.AGENTREMOTE.AI.INVALID_RELOGIN_TARGET" {
		t.Fatalf("unexpected errcode: %q", respErr.ErrCode)
	}
}

func TestOpenAILoginStartWithOverrideRejectsManagedBeeperRelogin(t *testing.T) {
	mxid := id.UserID("@alice:example.com")
	login := &OpenAILogin{User: &bridgev2.User{User: &database.User{MXID: mxid}}}
	old := &bridgev2.UserLogin{UserLogin: &database.UserLogin{ID: managedBeeperLoginID(mxid), UserMXID: mxid}}
	_, err := login.StartWithOverride(context.Background(), old)
	var respErr bridgev2.RespError
	if !errors.As(err, &respErr) {
		t.Fatalf("expected RespError, got %T", err)
	}
	if respErr.ErrCode != "COM.BEEPER.AGENTREMOTE.AI.MANAGED_BEEPER_RELOGIN_FORBIDDEN" {
		t.Fatalf("unexpected errcode: %q", respErr.ErrCode)
	}
}

func TestOpenAILoginFinishLoginRejectsProviderMismatch(t *testing.T) {
	mxid := id.UserID("@alice:example.com")
	login := &OpenAILogin{
		User: &bridgev2.User{User: &database.User{MXID: mxid}},
		Override: &bridgev2.UserLogin{
			UserLogin: &database.UserLogin{
				ID:       "login",
				UserMXID: mxid,
				Metadata: &UserLoginMetadata{Provider: ProviderOpenRouter},
			},
		},
	}
	_, err := login.finishLogin(context.Background(), ProviderOpenAI, "key", "", nil)
	var respErr bridgev2.RespError
	if !errors.As(err, &respErr) {
		t.Fatalf("expected RespError, got %T", err)
	}
	if respErr.ErrCode != "COM.BEEPER.AGENTREMOTE.AI.PROVIDER_MISMATCH" {
		t.Fatalf("unexpected errcode: %q", respErr.ErrCode)
	}
}
