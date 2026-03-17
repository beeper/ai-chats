package dummybridge

import (
	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote"
	bridgesdk "github.com/beeper/agentremote/sdk"
)

type UserLoginMetadata struct {
	Provider       string `json:"provider,omitempty"`
	AcceptedString string `json:"accepted_string,omitempty"`
	NextChatIndex  int    `json:"next_chat_index,omitempty"`
}

type PortalMetadata struct {
	Title             string                      `json:"title,omitempty"`
	Topic             string                      `json:"topic,omitempty"`
	ChatIndex         int                         `json:"chat_index,omitempty"`
	IsDummyBridgeRoom bool                        `json:"is_dummybridge_room,omitempty"`
	SDK               bridgesdk.SDKPortalMetadata `json:"sdk,omitempty"`
}

type GhostMetadata struct{}

type MessageMetadata struct {
	agentremote.BaseMessageMetadata
	Command  string `json:"command,omitempty"`
	Scenario string `json:"scenario,omitempty"`
}

func loginMetadata(login *bridgev2.UserLogin) *UserLoginMetadata {
	return agentremote.EnsureLoginMetadata[UserLoginMetadata](login)
}

func portalMeta(portal *bridgev2.Portal) *PortalMetadata {
	return agentremote.EnsurePortalMetadata[PortalMetadata](portal)
}

func (pm *PortalMetadata) GetSDKPortalMetadata() *bridgesdk.SDKPortalMetadata {
	if pm == nil {
		return nil
	}
	return &pm.SDK
}

func (pm *PortalMetadata) SetSDKPortalMetadata(meta *bridgesdk.SDKPortalMetadata) {
	if pm == nil || meta == nil {
		return
	}
	pm.SDK = *meta
}
