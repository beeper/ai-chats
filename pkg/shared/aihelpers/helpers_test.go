package aihelpers

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"
)

func TestApplyAIChatsBridgeInfoRoomTypes(t *testing.T) {
	cases := []struct {
		name     string
		roomType database.RoomType
		want     string
	}{
		{name: "dm", roomType: database.RoomTypeDM, want: "dm"},
		{name: "default", roomType: database.RoomTypeDefault, want: "group"},
		{name: "space", roomType: database.RoomTypeSpace, want: "space"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			content := &event.BridgeEventContent{}
			ApplyAIChatsBridgeInfo(content, "", tc.roomType)
			if content.BeeperRoomTypeV2 != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, content.BeeperRoomTypeV2)
			}
		})
	}
}

func TestApplyAIChatsBridgeInfo(t *testing.T) {
	content := &event.BridgeEventContent{}
	ApplyAIChatsBridgeInfo(content, "ai-codex", database.RoomTypeDM)

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
