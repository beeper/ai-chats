package sdk

import (
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"
)

const AIRoomKindAgent = "agent"

func ApplyAgentRemoteBridgeInfo(content *event.BridgeEventContent, protocolID string, roomType database.RoomType, aiKind string) {
	if content == nil {
		return
	}
	if protocolID != "" {
		content.Protocol.ID = protocolID
	}
	if aiKind != "" && aiKind != AIRoomKindAgent {
		content.BeeperRoomTypeV2 = "group"
		return
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
