package bridgeadapter

import "maunium.net/go/mautrix/bridgev2"

// DefaultNetworkCapabilities returns the common baseline capabilities for bridge connectors.
func DefaultNetworkCapabilities() *bridgev2.NetworkGeneralCapabilities {
	return &bridgev2.NetworkGeneralCapabilities{
		DisappearingMessages: true,
		Provisioning: bridgev2.ProvisioningCapabilities{
			ResolveIdentifier: bridgev2.ResolveIdentifierCapabilities{
				CreateDM:       true,
				LookupUsername: true,
				ContactList:    true,
				Search:         true,
			},
		},
	}
}

// DefaultBridgeInfoVersion returns the shared bridge info/capability schema version pair.
func DefaultBridgeInfoVersion() (info, capabilities int) {
	return 1, 3
}
