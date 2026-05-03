package ai

import (
	"strings"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/agentremote/sdk"
)

const aiBridgeProtocolID = "ai"

func aiBridgeProtocolIDForPortal(portal *bridgev2.Portal) string {
	if portal == nil {
		return aiBridgeProtocolID
	}
	loginID := strings.TrimSpace(string(portal.Receiver))
	provider, _, _ := strings.Cut(loginID, ":")
	if provider == "beeper" {
		return "beeper"
	}
	return aiBridgeProtocolID
}

func applyAIChatsBridgeInfo(portal *bridgev2.Portal, meta *PortalMetadata, content *event.BridgeEventContent) {
	if portal == nil {
		return
	}
	sdk.ApplyAgentRemoteBridgeInfo(content, aiBridgeProtocolIDForPortal(portal), portal.RoomType, aiPortalKind(meta))
}

func aiPortalKind(meta *PortalMetadata) string {
	if meta != nil && meta.InternalRoom() {
		return strings.TrimSpace(meta.InternalRoomKind)
	}
	return "chat"
}
