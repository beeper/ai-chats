package sdk

import (
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"
)

func ApplyAgentRemoteBridgeInfo(content *event.BridgeEventContent, protocolID string, roomType database.RoomType) {
	if content == nil {
		return
	}
	if protocolID != "" {
		content.Protocol.ID = protocolID
	}
	switch roomType {
	case database.RoomTypeDM:
		content.BeeperRoomTypeV2 = "dm"
	case database.RoomTypeSpace:
		content.BeeperRoomTypeV2 = "space"
	default:
		content.BeeperRoomTypeV2 = "group"
	}
}
