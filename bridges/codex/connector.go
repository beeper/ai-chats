package codex

import (
	"strings"
	"sync"
	"time"

	"go.mau.fi/util/dbutil"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/sdk"
)

var (
	_ bridgev2.NetworkConnector               = (*CodexConnector)(nil)
	_ bridgev2.PortalBridgeInfoFillingNetwork = (*CodexConnector)(nil)
)

// CodexConnector runs the dedicated Codex bridge surface.
type CodexConnector struct {
	*sdk.ConnectorBase
	br        *bridgev2.Bridge
	Config    Config
	sdkConfig *sdk.Config[*CodexClient, *Config]
	db        *dbutil.Database

	clientsMu sync.Mutex
	clients   map[networkid.UserLoginID]bridgev2.NetworkAPI
}

const (
	FlowCodexAPIKey                = "codex_api_key"
	FlowCodexChatGPT               = "codex_chatgpt"
	FlowCodexChatGPTExternalTokens = "codex_chatgpt_external_tokens"
	hostAuthLoginPrefix            = "codex_host"
)

func (cc *CodexConnector) bridgeDB() *dbutil.Database {
	return cc.db
}

func (cc *CodexConnector) hostAuthLoginID(mxid id.UserID) networkid.UserLoginID {
	return sdk.MakeUserLoginID(hostAuthLoginPrefix, mxid, 1)
}

func hasManagedCodexLogin(logins []*bridgev2.UserLogin, exceptID networkid.UserLoginID) bool {
	for _, existing := range logins {
		if existing == nil || existing.ID == exceptID || existing.Metadata == nil {
			continue
		}
		meta, ok := existing.Metadata.(*UserLoginMetadata)
		if !ok || meta == nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(meta.Provider), ProviderCodex) && isManagedAuthLogin(meta) {
			return true
		}
	}
	return false
}

func resolveCodexCommandFromConfig(cfg *CodexConfig) string {
	if cfg == nil {
		return "codex"
	}
	if cmd := strings.TrimSpace(cfg.Command); cmd != "" {
		return cmd
	}
	return "codex"
}

func (cc *CodexConnector) resolveCodexCommand() string {
	if cc == nil {
		return "codex"
	}
	return resolveCodexCommandFromConfig(cc.Config.Codex)
}

func (cc *CodexConnector) applyRuntimeDefaults() {
	if cc.Config.ModelCacheDuration == 0 {
		cc.Config.ModelCacheDuration = 6 * time.Hour
	}
	sdk.ApplyDefaultCommandPrefix(&cc.Config.Bridge.CommandPrefix, "!ai")
	if cc.Config.Codex == nil {
		cc.Config.Codex = &CodexConfig{}
	}
	sdk.ApplyBoolDefault(&cc.Config.Codex.Enabled, true)
	if strings.TrimSpace(cc.Config.Codex.Command) == "" {
		cc.Config.Codex.Command = "codex"
	}
	if strings.TrimSpace(cc.Config.Codex.DefaultModel) == "" {
		cc.Config.Codex.DefaultModel = "gpt-5.1-codex"
	}
	sdk.ApplyBoolDefault(&cc.Config.Codex.NetworkAccess, true)
	if cc.Config.Codex.ClientInfo == nil {
		cc.Config.Codex.ClientInfo = &CodexClientInfo{}
	}
	if strings.TrimSpace(cc.Config.Codex.ClientInfo.Name) == "" {
		cc.Config.Codex.ClientInfo.Name = "ai_bridge_matrix"
	}
	if strings.TrimSpace(cc.Config.Codex.ClientInfo.Title) == "" {
		cc.Config.Codex.ClientInfo.Title = "AI Bridge (Matrix)"
	}
	if strings.TrimSpace(cc.Config.Codex.ClientInfo.Version) == "" {
		cc.Config.Codex.ClientInfo.Version = "0.1.0"
	}
}

func (cc *CodexConnector) codexEnabled() bool {
	if cc.Config.Codex == nil || cc.Config.Codex.Enabled == nil {
		return true
	}
	return *cc.Config.Codex.Enabled
}
