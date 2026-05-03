package dummybridge

import (
	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote/sdk"
)

type UserLoginMetadata struct {
	Provider       string `json:"provider,omitempty"`
	AcceptedString string `json:"accepted_string,omitempty"`
}

type PortalMetadata struct {
	Title             string `json:"title,omitempty"`
	Topic             string `json:"topic,omitempty"`
	ChatIndex         int    `json:"chat_index,omitempty"`
	IsDummyBridgeRoom bool   `json:"is_dummybridge_room,omitempty"`
}

type GhostMetadata struct{}

type MessageMetadata struct {
	sdk.BaseMessageMetadata
	Command  string `json:"command,omitempty"`
	Scenario string `json:"scenario,omitempty"`
}

func loginMetadata(login *bridgev2.UserLogin) *UserLoginMetadata {
	return sdk.EnsureLoginMetadata[UserLoginMetadata](login)
}

func portalMeta(portal *bridgev2.Portal) *PortalMetadata {
	return sdk.EnsurePortalMetadata[PortalMetadata](portal)
}
