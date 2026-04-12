package ai

import (
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/agentremote/sdk"
)

const aiBridgeProtocolID = "ai"

func applyAIChatsBridgeInfo(portal *bridgev2.Portal, meta *PortalMetadata, content *event.BridgeEventContent) {
	if portal == nil {
		return
	}
	sdk.ApplyAgentRemoteBridgeInfo(content, aiBridgeProtocolID, portal.RoomType, integrationPortalAIKind(meta))
}
