package opencode

import (
	"github.com/beeper/ai-bridge/pkg/bridgeadapter"
	"go.mau.fi/util/jsontime"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"

	"github.com/beeper/ai-bridge/bridges/opencode/opencodebridge"
)

type UserLoginMetadata struct {
	Provider          string                                      `json:"provider,omitempty"`
	OpenCodeInstances map[string]*opencodebridge.OpenCodeInstance `json:"opencode_instances,omitempty"`
}

type PortalMetadata struct {
	Title                string `json:"title,omitempty"`
	TitleGenerated       bool   `json:"title_generated,omitempty"`
	IsOpenCodeRoom       bool   `json:"is_opencode_room,omitempty"`
	OpenCodeInstanceID   string `json:"opencode_instance_id,omitempty"`
	OpenCodeSessionID    string `json:"opencode_session_id,omitempty"`
	OpenCodeReadOnly     bool   `json:"opencode_read_only,omitempty"`
	OpenCodeTitlePending bool   `json:"opencode_title_pending,omitempty"`
	AgentID              string `json:"agent_id,omitempty"`
	VerboseLevel         string `json:"verbose_level,omitempty"`
}

type MessageMetadata struct{}

type GhostMetadata struct {
	LastSync jsontime.Unix `json:"last_sync,omitempty"`
}

func loginMetadata(login *bridgev2.UserLogin) *UserLoginMetadata {
	return bridgeadapter.EnsureLoginMetadata[UserLoginMetadata](login)
}

func portalMeta(portal *bridgev2.Portal) *PortalMetadata {
	return bridgeadapter.EnsurePortalMetadata[PortalMetadata](portal)
}

func humanUserID(loginID networkid.UserLoginID) networkid.UserID {
	return networkid.UserID("opencode-user:" + string(loginID))
}
