package ai

import (
	"testing"

	"maunium.net/go/mautrix/bridgev2/status"
	"maunium.net/go/mautrix/event"
)

func TestBridgeStateForError_AccessDenied403(t *testing.T) {
	err := testOpenAIError(403, "access_denied", "invalid_request_error", "This feature requires the bridge:ai feature flag")
	state, shouldMarkLoggedOut, ok := bridgeStateForError(err)
	if !ok {
		t.Fatal("expected bridge state for access_denied")
	}
	if shouldMarkLoggedOut {
		t.Fatal("expected access_denied to keep login active")
	}
	if state.StateEvent != status.StateUnknownError {
		t.Fatalf("expected unknown error state, got %s", state.StateEvent)
	}
	if state.Error != AIProviderError {
		t.Fatalf("expected provider error code, got %s", state.Error)
	}
	if state.Message != "This feature requires the bridge:ai feature flag" {
		t.Fatalf("unexpected state message: %q", state.Message)
	}
}

func TestBridgeStateForError_Auth403(t *testing.T) {
	err := testOpenAIError(403, "forbidden", "authentication_error", "invalid api key")
	state, shouldMarkLoggedOut, ok := bridgeStateForError(err)
	if !ok {
		t.Fatal("expected bridge state for auth failure")
	}
	if !shouldMarkLoggedOut {
		t.Fatal("expected auth failure to mark login inactive")
	}
	if state.StateEvent != status.StateBadCredentials {
		t.Fatalf("expected bad credentials state, got %s", state.StateEvent)
	}
}

func TestMessageStatusReasonForError_AccessDenied403(t *testing.T) {
	err := testOpenAIError(403, "access_denied", "invalid_request_error", "This feature requires the bridge:ai feature flag")
	if got := messageStatusForError(err); got != event.MessageStatusFail {
		t.Fatalf("expected fail status, got %s", got)
	}
	if got := messageStatusReasonForError(err); got != event.MessageStatusNoPermission {
		t.Fatalf("expected no-permission reason, got %s", got)
	}
}
