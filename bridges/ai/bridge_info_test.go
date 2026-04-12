package ai

import (
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
)

func TestIntegrationPortalAIKind(t *testing.T) {
	t.Run("visible rooms are agent", func(t *testing.T) {
		if got := integrationPortalAIKind(nil); got != "agent" {
			t.Fatalf("expected agent room kind, got %q", got)
		}
	})

	t.Run("subagent rooms are subagent", func(t *testing.T) {
		meta := &PortalMetadata{SubagentParentRoomID: "!parent:example.com"}
		if got := integrationPortalAIKind(meta); got != "subagent" {
			t.Fatalf("expected subagent room kind, got %q", got)
		}
	})

	t.Run("internal module rooms use module name", func(t *testing.T) {
		meta := &PortalMetadata{
			ModuleMeta: map[string]any{
				"cron": map[string]any{"is_internal_room": true},
			},
		}
		if got := integrationPortalAIKind(meta); got != "cron" {
			t.Fatalf("expected cron room kind, got %q", got)
		}
	})
}

func TestApplyAIChatsBridgeInfo(t *testing.T) {
	t.Run("visible dm rooms stay dm", func(t *testing.T) {
		portal := &bridgev2.Portal{Portal: &database.Portal{
			RoomType: database.RoomTypeDM,
			PortalKey: networkid.PortalKey{
				Receiver: networkid.UserLoginID("openrouter:@user:example.com"),
			},
		}}
		content := &event.BridgeEventContent{}

		applyAIChatsBridgeInfo(portal, nil, content)

		if content.Protocol.ID != aiBridgeProtocolID {
			t.Fatalf("expected protocol id %q, got %q", aiBridgeProtocolID, content.Protocol.ID)
		}
		if content.BeeperRoomTypeV2 != "dm" {
			t.Fatalf("expected dm room type, got %q", content.BeeperRoomTypeV2)
		}
	})

	t.Run("beeper rooms use beeper protocol id", func(t *testing.T) {
		portal := &bridgev2.Portal{Portal: &database.Portal{
			RoomType: database.RoomTypeDM,
			PortalKey: networkid.PortalKey{
				Receiver: networkid.UserLoginID("beeper:@user:beeper.local"),
			},
		}}
		content := &event.BridgeEventContent{}

		applyAIChatsBridgeInfo(portal, nil, content)

		if content.Protocol.ID != "beeper" {
			t.Fatalf("expected protocol id %q, got %q", "beeper", content.Protocol.ID)
		}
	})

	t.Run("background rooms normalize to group", func(t *testing.T) {
		portal := &bridgev2.Portal{Portal: &database.Portal{
			RoomType: database.RoomTypeDM,
			PortalKey: networkid.PortalKey{
				Receiver: networkid.UserLoginID("beeper:@user:beeper.local"),
			},
		}}
		meta := &PortalMetadata{
			ModuleMeta: map[string]any{
				"heartbeat": map[string]any{"is_internal_room": true},
			},
		}
		content := &event.BridgeEventContent{}

		applyAIChatsBridgeInfo(portal, meta, content)

		if content.BeeperRoomTypeV2 != "group" {
			t.Fatalf("expected group room type, got %q", content.BeeperRoomTypeV2)
		}
	})
}
