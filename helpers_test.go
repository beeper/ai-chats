package agentremote

import (
	"testing"

	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"
)

func TestNormalizeAIRoomTypeV2(t *testing.T) {
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
			if got := NormalizeAIRoomTypeV2(tc.roomType, tc.aiKind); got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

func TestApplyAIBridgeInfo(t *testing.T) {
	content := &event.BridgeEventContent{}
	ApplyAIBridgeInfo(content, "ai-codex", database.RoomTypeDM, AIRoomKindAgent)

	if content.Protocol.ID != "ai-codex" {
		t.Fatalf("expected protocol id ai-codex, got %q", content.Protocol.ID)
	}
	if content.BeeperRoomTypeV2 != "dm" {
		t.Fatalf("expected dm room type, got %q", content.BeeperRoomTypeV2)
	}
}
