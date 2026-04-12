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
	switch provider {
	case "beeper":
		// Beeper clients know the Beeper Cloud bridge; the generic "ai" protocol
		// shows up as an unknown bridge in local Beeper-backed rooms.
		return "beeper"
	default:
		return aiBridgeProtocolID
	}
}

func applyAgentRemoteBridgeInfo(portal *bridgev2.Portal, meta *PortalMetadata, content *event.BridgeEventContent) {
	if portal == nil {
		return
	}
	sdk.ApplyAgentRemoteBridgeInfo(content, aiBridgeProtocolIDForPortal(portal), portal.RoomType, integrationPortalAIKind(meta))
}
