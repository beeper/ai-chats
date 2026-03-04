package connector

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRoomSettingsEventContentUnmarshalAgentID(t *testing.T) {
	var content RoomSettingsEventContent
	if err := json.Unmarshal([]byte(`{"agent_id":"beeper"}`), &content); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if content.AgentID != "beeper" {
		t.Fatalf("expected agent_id to populate AgentID, got %q", content.AgentID)
	}
}

func TestRoomSettingsEventContentMarshalUsesCanonicalAgentID(t *testing.T) {
	raw, err := json.Marshal(RoomSettingsEventContent{
		Model:   "openai/gpt-5",
		AgentID: "beeper",
	})
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	encoded := string(raw)
	if !strings.Contains(encoded, `"agent_id":"beeper"`) {
		t.Fatalf("expected canonical agent_id field, got %s", encoded)
	}
}
