package main

import (
	"strings"
	"testing"
)

func TestEnsureProfileDeviceIDPersists(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	first, err := ensureProfileDeviceID(defaultProfile)
	if err != nil {
		t.Fatalf("ensureProfileDeviceID returned error: %v", err)
	}
	if len(first) != 10 {
		t.Fatalf("expected 10-char device id, got %q", first)
	}

	second, err := ensureProfileDeviceID(defaultProfile)
	if err != nil {
		t.Fatalf("ensureProfileDeviceID second call returned error: %v", err)
	}
	if first != second {
		t.Fatalf("expected stable device id, got %q then %q", first, second)
	}

	state, err := loadProfileState(defaultProfile)
	if err != nil {
		t.Fatalf("loadProfileState returned error: %v", err)
	}
	if state.DeviceID != first {
		t.Fatalf("expected persisted device id %q, got %q", first, state.DeviceID)
	}
}

func TestLoadAuthConfigWithoutAuthReturnsMissing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	if err := saveProfileState(defaultProfile, &profileState{DeviceID: "abc123def0"}); err != nil {
		t.Fatalf("saveProfileState returned error: %v", err)
	}

	_, err := loadAuthConfig(defaultProfile)
	if err == nil {
		t.Fatal("expected missing auth error")
	}
	if !strings.Contains(err.Error(), "not logged in") {
		t.Fatalf("expected not logged in error, got %v", err)
	}
}

func TestSaveAuthConfigPreservesDeviceID(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	const deviceID = "abc123def0"
	if err := saveProfileState(defaultProfile, &profileState{DeviceID: deviceID}); err != nil {
		t.Fatalf("saveProfileState returned error: %v", err)
	}

	cfg := authConfig{
		Env:      "prod",
		Username: "alice",
		Token:    "token-123",
	}
	if err := saveAuthConfig(defaultProfile, cfg); err != nil {
		t.Fatalf("saveAuthConfig returned error: %v", err)
	}

	state, err := loadProfileState(defaultProfile)
	if err != nil {
		t.Fatalf("loadProfileState returned error: %v", err)
	}
	if state.DeviceID != deviceID {
		t.Fatalf("expected device id %q, got %q", deviceID, state.DeviceID)
	}
	if state.Auth == nil {
		t.Fatal("expected auth to be stored")
	}
	if state.Auth.Domain != "beeper.com" {
		t.Fatalf("expected domain beeper.com, got %q", state.Auth.Domain)
	}
	if state.Auth.Username != "alice" {
		t.Fatalf("expected username alice, got %q", state.Auth.Username)
	}
}
