package ai

import (
	"encoding/base64"
	"strings"
	"testing"

	"maunium.net/go/mautrix/bridgev2/status"
	"maunium.net/go/mautrix/event"
)

func TestDecodeBase64Image(t *testing.T) {
	// Sample PNG header bytes (minimal valid PNG)
	pngBytes := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	pngBase64 := base64.StdEncoding.EncodeToString(pngBytes)

	// Sample JPEG header bytes
	jpegBytes := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 0x4A, 0x46, 0x49, 0x46}
	jpegBase64 := base64.StdEncoding.EncodeToString(jpegBytes)

	tests := []struct {
		name         string
		input        string
		wantErr      bool
		wantMimeType string
		errContains  string
	}{
		{
			name:         "raw base64 PNG",
			input:        pngBase64,
			wantErr:      false,
			wantMimeType: "image/png",
		},
		{
			name:         "data URL with PNG",
			input:        "data:image/png;base64," + pngBase64,
			wantErr:      false,
			wantMimeType: "image/png",
		},
		{
			name:         "data URL with JPEG",
			input:        "data:image/jpeg;base64," + jpegBase64,
			wantErr:      false,
			wantMimeType: "image/jpeg",
		},
		{
			name:         "data URL with webp",
			input:        "data:image/webp;base64," + pngBase64,
			wantErr:      false,
			wantMimeType: "image/webp",
		},
		{
			name:        "invalid data URL - no comma",
			input:       "data:image/png;base64" + pngBase64,
			wantErr:     true,
			errContains: "no comma separator",
		},
		{
			name:    "invalid base64",
			input:   "not-valid-base64!!!",
			wantErr: true,
		},
		{
			name:         "URL-safe base64",
			input:        base64.URLEncoding.EncodeToString(pngBytes),
			wantErr:      false,
			wantMimeType: "image/png",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, mimeType, err := decodeBase64Image(tt.input)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error but got nil")
				} else if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.errContains)
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if len(data) == 0 {
				t.Error("expected non-empty data")
			}

			if mimeType != tt.wantMimeType {
				t.Errorf("mimeType = %q, want %q", mimeType, tt.wantMimeType)
			}
		})
	}
}

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
