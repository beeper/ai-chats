package sdk

import (
	"errors"
	"testing"

	"maunium.net/go/mautrix/bridgev2"
)

func TestValidateLoginStateReturnsTypedErrors(t *testing.T) {
	err := ValidateLoginState(nil, &bridgev2.Bridge{})
	var respErr bridgev2.RespError
	if !errors.As(err, &respErr) {
		t.Fatalf("expected RespError, got %T", err)
	}
	if respErr.StatusCode != 500 {
		t.Fatalf("unexpected status code: %d", respErr.StatusCode)
	}
	if respErr.ErrCode != "COM.BEEPER.AI_CHATS.LOGIN.MISSING_USER_CONTEXT" {
		t.Fatalf("unexpected errcode: %q", respErr.ErrCode)
	}

	err = ValidateLoginState(&bridgev2.User{}, nil)
	if !errors.As(err, &respErr) {
		t.Fatalf("expected RespError, got %T", err)
	}
	if respErr.ErrCode != "COM.BEEPER.AI_CHATS.LOGIN.CONNECTOR_NOT_INITIALIZED" {
		t.Fatalf("unexpected errcode: %q", respErr.ErrCode)
	}
}
