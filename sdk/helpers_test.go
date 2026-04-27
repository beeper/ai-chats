package sdk

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"
)

func TestApplyAgentRemoteBridgeInfoRoomTypes(t *testing.T) {
	cases := []struct {
		name     string
		roomType database.RoomType
		aiKind   string
		want     string
	}{
		{name: "agent dm", roomType: database.RoomTypeDM, aiKind: AIRoomKindAgent, want: "dm"},
		{name: "agent default", roomType: database.RoomTypeDefault, aiKind: AIRoomKindAgent, want: "group"},
		{name: "agent space", roomType: database.RoomTypeSpace, aiKind: AIRoomKindAgent, want: "space"},
		{name: "subagent forced group", roomType: database.RoomTypeDM, aiKind: "subagent", want: "group"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			content := &event.BridgeEventContent{}
			ApplyAgentRemoteBridgeInfo(content, "", tc.roomType, tc.aiKind)
			if content.BeeperRoomTypeV2 != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, content.BeeperRoomTypeV2)
			}
		})
	}
}

func TestApplyAgentRemoteBridgeInfo(t *testing.T) {
	content := &event.BridgeEventContent{}
	ApplyAgentRemoteBridgeInfo(content, "ai-codex", database.RoomTypeDM, AIRoomKindAgent)

	if content.Protocol.ID != "ai-codex" {
		t.Fatalf("expected protocol id ai-codex, got %q", content.Protocol.ID)
	}
	if content.BeeperRoomTypeV2 != "dm" {
		t.Fatalf("expected dm room type, got %q", content.BeeperRoomTypeV2)
	}
}

func TestNewTurnIDIsOpaqueUUID(t *testing.T) {
	turnID := NewTurnID()
	otherID := NewTurnID()
	if turnID == "" || otherID == "" {
		t.Fatal("expected non-empty turn id")
	}
	if strings.HasPrefix(turnID, "turn_") {
		t.Fatalf("expected opaque turn id, got legacy prefix %q", turnID)
	}
	if turnID == otherID {
		t.Fatalf("expected unique turn ids, got %q twice", turnID)
	}
	if _, err := uuid.Parse(turnID); err != nil {
		t.Fatalf("expected uuid-shaped turn id, got %q: %v", turnID, err)
	}
}
