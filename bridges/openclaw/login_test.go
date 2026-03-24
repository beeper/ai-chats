package openclaw

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"maunium.net/go/mautrix/bridgev2"
)

func TestOpenClawLoginStartUsesSingleCredentialsStep(t *testing.T) {
	login := &OpenClawLogin{
		User:      &bridgev2.User{},
		Connector: &OpenClawConnector{br: &bridgev2.Bridge{}},
	}

	step, err := login.Start(context.Background())
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if step.StepID != openClawLoginStepCredentials {
		t.Fatalf("unexpected first step id: %q", step.StepID)
	}
	if step.UserInputParams == nil || len(step.UserInputParams.Fields) != 4 {
		t.Fatalf("expected four credential fields, got %#v", step.UserInputParams)
	}
	wantFieldIDs := []string{"url", "token", "password", "label"}
	for i, field := range step.UserInputParams.Fields {
		if field.ID != wantFieldIDs[i] {
			t.Fatalf("unexpected field order: got %q want %q", field.ID, wantFieldIDs[i])
		}
	}
}

func TestOpenClawLoginStartPrefillsDiscoveryValues(t *testing.T) {
	login := &OpenClawLogin{
		User:         &bridgev2.User{},
		Connector:    &OpenClawConnector{br: &bridgev2.Bridge{}},
		prefillURL:   "wss://gateway.local:443",
		prefillLabel: "Studio",
	}

	step, err := login.Start(context.Background())
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	fields := step.UserInputParams.Fields
	if fields[0].DefaultValue != "wss://gateway.local:443" {
		t.Fatalf("unexpected url default: %q", fields[0].DefaultValue)
	}
	if fields[3].DefaultValue != "Studio" {
		t.Fatalf("unexpected label default: %q", fields[3].DefaultValue)
	}
}

func TestNormalizeOpenClawAuthCredentials(t *testing.T) {
	token, password, err := normalizeOpenClawAuthCredentials(map[string]string{})
	if err != nil {
		t.Fatalf("unexpected error for no-auth input: %v", err)
	}
	if token != "" || password != "" {
		t.Fatalf("expected empty credentials, got token=%q password=%q", token, password)
	}

	token, password, err = normalizeOpenClawAuthCredentials(map[string]string{"token": "abc"})
	if err != nil {
		t.Fatalf("unexpected error for token input: %v", err)
	}
	if token != "abc" || password != "" {
		t.Fatalf("unexpected token credentials: token=%q password=%q", token, password)
	}

	token, password, err = normalizeOpenClawAuthCredentials(map[string]string{"password": "secret"})
	if err != nil {
		t.Fatalf("unexpected error for password input: %v", err)
	}
	if token != "" || password != "secret" {
		t.Fatalf("unexpected password credentials: token=%q password=%q", token, password)
	}

	_, _, err = normalizeOpenClawAuthCredentials(map[string]string{"token": "abc", "password": "secret"})
	if err == nil {
		t.Fatal("expected token+password input to fail")
	}
}

func TestOpenClawLoginSubmitUserInputRejectsTokenAndPassword(t *testing.T) {
	login := &OpenClawLogin{
		User:      &bridgev2.User{},
		Connector: &OpenClawConnector{br: &bridgev2.Bridge{}},
	}
	if _, err := login.Start(context.Background()); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}

	_, err := login.SubmitUserInput(context.Background(), map[string]string{
		"url":      "ws://127.0.0.1:18789",
		"token":    "shared-token",
		"password": "shared-password",
	})
	if err == nil {
		t.Fatal("expected SubmitUserInput to reject token+password")
	}
}

func TestOpenClawLoginSubmitUserInputPairingRequiredReturnsWaitStep(t *testing.T) {
	login := &OpenClawLogin{
		User:      &bridgev2.User{},
		Connector: &OpenClawConnector{br: &bridgev2.Bridge{}},
		preflight: func(context.Context, string, string, string) (string, error) {
			return "", &gatewayRPCError{
				Method:     "connect",
				Message:    "pairing required",
				DetailCode: "PAIRING_REQUIRED",
				RequestID:  "req-123",
			}
		},
	}
	if _, err := login.Start(context.Background()); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}

	step, err := login.SubmitUserInput(context.Background(), map[string]string{
		"url":   "ws://127.0.0.1:18789",
		"token": "shared-token",
	})
	if err != nil {
		t.Fatalf("SubmitUserInput returned error: %v", err)
	}
	if step.Type != bridgev2.LoginStepTypeDisplayAndWait {
		t.Fatalf("unexpected step type: %q", step.Type)
	}
	if step.StepID != openClawLoginStepPairingWait {
		t.Fatalf("unexpected step id: %q", step.StepID)
	}
	if step.DisplayAndWaitParams == nil || step.DisplayAndWaitParams.Type != bridgev2.LoginDisplayTypeNothing {
		t.Fatalf("unexpected display-and-wait params: %#v", step.DisplayAndWaitParams)
	}
	if !strings.Contains(step.Instructions, "req-123") {
		t.Fatalf("expected request ID in instructions, got %q", step.Instructions)
	}
	if login.step != openClawLoginStatePairingWait {
		t.Fatalf("unexpected login state: %q", login.step)
	}
	if login.pending == nil || login.pending.requestID != "req-123" {
		t.Fatalf("unexpected pending login: %#v", login.pending)
	}
}

func TestOpenClawLoginWaitReturnsStillWaitingStepOnContextDone(t *testing.T) {
	login := &OpenClawLogin{
		User:      &bridgev2.User{},
		Connector: &OpenClawConnector{br: &bridgev2.Bridge{}},
		step:      openClawLoginStatePairingWait,
		pending: &openClawPendingLogin{
			gatewayURL: "ws://127.0.0.1:18789",
			token:      "shared-token",
			requestID:  "req-456",
		},
		waitUntil: time.Now().Add(time.Minute),
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	step, err := login.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait returned error: %v", err)
	}
	if step.StepID != openClawLoginStepPairingWait {
		t.Fatalf("unexpected step id: %q", step.StepID)
	}
	if !strings.Contains(step.Instructions, "Still waiting") {
		t.Fatalf("expected still waiting instructions, got %q", step.Instructions)
	}
}

func TestOpenClawLoginWaitMapsNonPairingErrors(t *testing.T) {
	login := &OpenClawLogin{
		User:       &bridgev2.User{},
		Connector:  &OpenClawConnector{br: &bridgev2.Bridge{}},
		step:       openClawLoginStatePairingWait,
		pollEvery:  time.Millisecond,
		returnWait: time.Second,
		waitFor:    time.Second,
		pending: &openClawPendingLogin{
			gatewayURL: "ws://127.0.0.1:18789",
			token:      "shared-token",
			requestID:  "req-789",
		},
		preflight: func(context.Context, string, string, string) (string, error) {
			return "", &gatewayRPCError{
				Method:     "connect",
				Message:    "token mismatch",
				DetailCode: "AUTH_TOKEN_MISMATCH",
			}
		},
	}

	_, err := login.Wait(context.Background())
	if err == nil {
		t.Fatal("expected Wait to return an error")
	}
	var respErr bridgev2.RespError
	if !errors.As(err, &respErr) {
		t.Fatalf("expected RespError, got %T", err)
	}
	if respErr.StatusCode != 403 {
		t.Fatalf("unexpected status code: %d", respErr.StatusCode)
	}
}
