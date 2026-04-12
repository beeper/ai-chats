package sdk

import "maunium.net/go/mautrix/bridgev2"

// DefaultNetworkCapabilities returns an empty capability set.
func DefaultNetworkCapabilities() *bridgev2.NetworkGeneralCapabilities {
	return &bridgev2.NetworkGeneralCapabilities{}
}

// DefaultBridgeInfoVersion returns the shared bridge info/capability schema version pair.
func DefaultBridgeInfoVersion() (info, capabilities int) {
	return 1, 3
}
