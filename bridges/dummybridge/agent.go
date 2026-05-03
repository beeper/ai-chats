package dummybridge

import (
	"maunium.net/go/mautrix/bridgev2/networkid"

	"github.com/beeper/agentremote/sdk"
)

const (
	dummyAgentIdentifierPrimary = "dummybridge"
	dummyAgentIdentifierShort   = "dummy"
	dummyAgentName              = "DummyBridge"
)

var dummyAgentUserID = networkid.UserID(dummyAgentIdentifierPrimary)

func dummySDKAgent() *sdk.Agent {
	return &sdk.Agent{
		ID:          string(dummyAgentUserID),
		Name:        dummyAgentName,
		Description: "Synthetic demo agent for streaming, turns, tools, and approvals.",
		Identifiers: []string{
			dummyAgentIdentifierPrimary,
			dummyAgentIdentifierShort,
		},
		Capabilities: sdk.BaseAgentCapabilities(),
	}
}
