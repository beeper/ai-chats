package opencode

import (
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"

	"github.com/beeper/agentremote"
)

type UserLoginMetadata struct {
	Provider          string                       `json:"provider,omitempty"`
	OpenCodeInstances map[string]*OpenCodeInstance `json:"opencode_instances,omitempty"`
}

type PortalMetadata struct {
	Title                string `json:"title,omitempty"`
	TitleGenerated       bool   `json:"title_generated,omitempty"`
	IsOpenCodeRoom       bool   `json:"is_opencode_room,omitempty"`
	OpenCodeInstanceID   string `json:"opencode_instance_id,omitempty"`
	OpenCodeSessionID    string `json:"opencode_session_id,omitempty"`
	OpenCodeReadOnly     bool   `json:"opencode_read_only,omitempty"`
	OpenCodeTitlePending bool   `json:"opencode_title_pending,omitempty"`
	OpenCodeAwaitingPath bool   `json:"opencode_awaiting_path,omitempty"`
	AgentID              string `json:"agent_id,omitempty"`
	VerboseLevel         string `json:"verbose_level,omitempty"`
}

type GhostMetadata struct{}

func loginMetadata(login *bridgev2.UserLogin) *UserLoginMetadata {
	return agentremote.EnsureLoginMetadata[UserLoginMetadata](login)
}

func portalMeta(portal *bridgev2.Portal) *PortalMetadata {
	return agentremote.EnsurePortalMetadata[PortalMetadata](portal)
}

func humanUserID(loginID networkid.UserLoginID) networkid.UserID {
	return agentremote.HumanUserID("opencode-user", loginID)
}
