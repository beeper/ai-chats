package helpers

import (
	"context"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote/sdk"
)

// BroadcastRoomCapabilities sends room capability state events for the given conversation.
func BroadcastRoomCapabilities(ctx context.Context, conv *sdk.Conversation, features *sdk.RoomFeatures) error {
	if features != nil {
		return conv.BroadcastCapabilities(ctx)
	}
	return conv.BroadcastCapabilities(ctx)
}

// BroadcastCommandDescriptions sends MSC4391 command-description state events
// for all SDK commands into the given room.
func BroadcastCommandDescriptions(ctx context.Context, conv *sdk.Conversation, commands []sdk.Command) error {
	portal := conv.Portal()
	if portal == nil || portal.MXID == "" {
		return nil
	}
	login := conv.Login()
	if login == nil || login.Bridge == nil || login.Bridge.Bot == nil {
		return nil
	}
	bot := login.Bridge.Bot
	sdk.BroadcastCommandDescriptions(ctx, portal, bot, commands)
	return nil
}

// BroadcastRoomState sends both room capabilities and command descriptions.
func BroadcastRoomState(ctx context.Context, conv *sdk.Conversation, features *sdk.RoomFeatures, commands []sdk.Command) error {
	if err := BroadcastRoomCapabilities(ctx, conv, features); err != nil {
		return err
	}
	return BroadcastCommandDescriptions(ctx, conv, commands)
}

// UpdatePortalCapabilities refreshes the Matrix room capabilities for a portal.
func UpdatePortalCapabilities(ctx context.Context, portal *bridgev2.Portal, login *bridgev2.UserLogin) {
	if portal != nil {
		portal.UpdateCapabilities(ctx, login, false)
	}
}
