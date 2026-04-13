package sdk

import (
	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"
)

const AIRoomKindAgent = "agent"

func BuildBotUserInfo(name string, identifiers ...string) *bridgev2.UserInfo {
	return &bridgev2.UserInfo{
		Name:        ptr.Ptr(name),
		IsBot:       ptr.Ptr(true),
		Identifiers: identifiers,
	}
}

func NormalizeAIRoomTypeV2(roomType database.RoomType, aiKind string) string {
	if aiKind != "" && aiKind != AIRoomKindAgent {
		return "group"
	}
	switch roomType {
	case database.RoomTypeDM:
		return "dm"
	case database.RoomTypeSpace:
		return "space"
	default:
		return "group"
	}
}

func ApplyAgentRemoteBridgeInfo(content *event.BridgeEventContent, protocolID string, roomType database.RoomType, aiKind string) {
	if content == nil {
		return
	}
	if protocolID != "" {
		content.Protocol.ID = protocolID
	}
	content.BeeperRoomTypeV2 = NormalizeAIRoomTypeV2(roomType, aiKind)
}
