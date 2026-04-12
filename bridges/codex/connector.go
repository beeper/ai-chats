package codex

import (
	"strings"
	"sync"
	"time"

	"go.mau.fi/util/dbutil"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"

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
)

func (cc *CodexConnector) bridgeDB() *dbutil.Database {
	return cc.db
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
		cc.Config.Codex.ClientInfo.Name = defaultCodexClientInfoName
	}
	if strings.TrimSpace(cc.Config.Codex.ClientInfo.Title) == "" {
		cc.Config.Codex.ClientInfo.Title = defaultCodexClientInfoTitle
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
