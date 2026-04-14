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
	if respErr.ErrCode != "COM.BEEPER.AGENTREMOTE.LOGIN.MISSING_USER_CONTEXT" {
		t.Fatalf("unexpected errcode: %q", respErr.ErrCode)
	}

	err = ValidateLoginState(&bridgev2.User{}, nil)
	if !errors.As(err, &respErr) {
		t.Fatalf("expected RespError, got %T", err)
	}
	if respErr.ErrCode != "COM.BEEPER.AGENTREMOTE.LOGIN.CONNECTOR_NOT_INITIALIZED" {
		t.Fatalf("unexpected errcode: %q", respErr.ErrCode)
	}
}

func TestValidateLoginFlowReturnsTypedErrors(t *testing.T) {
	if err := ValidateLoginFlow("wrong", true, "disabled", "LOGIN", "DISABLED", func(flowID string) bool {
		return flowID == "expected"
	}); !errors.Is(err, bridgev2.ErrInvalidLoginFlowID) {
		t.Fatalf("expected invalid login flow error, got %v", err)
	}

	err := ValidateLoginFlow("expected", false, "disabled", "LOGIN", "DISABLED", func(flowID string) bool {
		return flowID == "expected"
	})
	var respErr bridgev2.RespError
	if !errors.As(err, &respErr) {
		t.Fatalf("expected RespError, got %T", err)
	}
	if respErr.StatusCode != 403 {
		t.Fatalf("unexpected status code: %d", respErr.StatusCode)
	}
	if respErr.ErrCode != "COM.BEEPER.AGENTREMOTE.LOGIN.DISABLED" {
		t.Fatalf("unexpected errcode: %q", respErr.ErrCode)
	}

	if err := ValidateLoginFlow("expected", true, "disabled", "LOGIN", "DISABLED", func(flowID string) bool {
		return flowID == "expected"
	}); err != nil {
		t.Fatalf("expected valid flow, got %v", err)
	}
}
