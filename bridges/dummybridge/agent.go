package dummybridge

import (
	bridgesdk "github.com/beeper/agentremote/sdk"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

const (
	dummyAgentIdentifierPrimary = "dummybridge"
	dummyAgentIdentifierShort   = "dummy"
	dummyAgentName              = "DummyBridge"
)

var dummyAgentUserID = networkid.UserID(dummyAgentIdentifierPrimary)

func dummySDKAgent() *bridgesdk.Agent {
	return &bridgesdk.Agent{
		ID:          string(dummyAgentUserID),
		Name:        dummyAgentName,
		Description: "Synthetic demo agent for streaming, turns, tools, and approvals.",
		Identifiers: []string{
			dummyAgentIdentifierPrimary,
			dummyAgentIdentifierShort,
		},
		Capabilities: bridgesdk.BaseAgentCapabilities(),
	}
}
