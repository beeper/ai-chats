package agents

import (
	"encoding/json"
	"testing"
)

func TestGetPresetByIDPlaygroundIsNotAPreset(t *testing.T) {
	preset := GetPresetByID("playground")
	if preset != nil {
		t.Fatalf("expected no preset for playground, got %q", preset.ID)
	}
}

func TestResponseModeUnmarshalRawStaysRaw(t *testing.T) {
	var decoded struct {
		ResponseMode ResponseMode `json:"response_mode"`
	}

	if err := json.Unmarshal([]byte(`{"response_mode":"raw"}`), &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if decoded.ResponseMode != ResponseMode("raw") {
		t.Fatalf("expected %q, got %q", ResponseMode("raw"), decoded.ResponseMode)
	}
}
