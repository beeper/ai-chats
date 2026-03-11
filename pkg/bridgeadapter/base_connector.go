package bridgeadapter

import (
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

// BaseConnectorMethods is an embeddable mixin that provides default
// implementations for common NetworkConnector methods. Bridges can override
// individual methods when they need different behaviour (e.g. OpenClaw
// overrides GetCapabilities to disable disappearing messages).
type BaseConnectorMethods struct {
	ProtocolID string // e.g. "ai-opencode"
}

func (b BaseConnectorMethods) GetBridgeInfoVersion() (info, capabilities int) {
	return DefaultBridgeInfoVersion()
}

func (b BaseConnectorMethods) GetCapabilities() *bridgev2.NetworkGeneralCapabilities {
	return DefaultNetworkCapabilities()
}

func (b BaseConnectorMethods) FillPortalBridgeInfo(portal *bridgev2.Portal, content *event.BridgeEventContent) {
	if portal == nil {
		return
	}
	ApplyAIBridgeInfo(content, b.ProtocolID, portal.RoomType, AIRoomKindAgent)
}
