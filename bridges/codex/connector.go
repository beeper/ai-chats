package codex

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"go.mau.fi/util/configupgrade"
	"go.mau.fi/util/dbutil"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/pkg/aidb"
	"github.com/beeper/agentremote/sdk"
)

var (
	_ bridgev2.NetworkConnector               = (*CodexConnector)(nil)
	_ bridgev2.PortalBridgeInfoFillingNetwork = (*CodexConnector)(nil)
)

// CodexConnector runs the dedicated Codex bridge surface.
type CodexConnector struct {
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

func codexLoginFlows() []bridgev2.LoginFlow {
	return []bridgev2.LoginFlow{
		{
			ID:          FlowCodexAPIKey,
			Name:        "API Key",
			Description: "Sign in with an OpenAI API key using codex app-server.",
		},
		{
			ID:          FlowCodexChatGPT,
			Name:        "ChatGPT",
			Description: "Open browser login and authenticate with your ChatGPT account.",
		},
		{
			ID:          FlowCodexChatGPTExternalTokens,
			Name:        "ChatGPT external tokens",
			Description: "Provide externally managed ChatGPT id/access tokens.",
		},
	}
}

func (cc *CodexConnector) bridgeDB() *dbutil.Database {
	return cc.db
}

func (cc *CodexConnector) Init(bridge *bridgev2.Bridge) {
	cc.br = bridge
	cc.clientsMu.Lock()
	if cc.clients == nil {
		cc.clients = make(map[networkid.UserLoginID]bridgev2.NetworkAPI)
	}
	cc.clientsMu.Unlock()
	if bridge != nil && bridge.DB != nil && bridge.DB.Database != nil {
		cc.db = aidb.NewChild(
			bridge.DB.Database,
			dbutil.ZeroLogger(bridge.Log.With().Str("db_section", "codex_bridge").Logger()),
		)
	}
}

func (cc *CodexConnector) Start(ctx context.Context) error {
	db := cc.bridgeDB()
	if db == nil {
		return fmt.Errorf("codex database not initialized")
	}
	if err := db.Upgrade(ctx); err != nil {
		return err
	}
	cc.applyRuntimeDefaults()
	return nil
}

func (cc *CodexConnector) Stop() {
	cc.clientsMu.Lock()
	clients := maps.Clone(cc.clients)
	cc.clientsMu.Unlock()
	for _, client := range clients {
		client.Disconnect()
	}
}

func (cc *CodexConnector) GetName() bridgev2.BridgeName {
	defaultCommandPrefix := "!codex"
	if trimmed := strings.TrimSpace(cc.Config.Bridge.CommandPrefix); trimmed != "" {
		defaultCommandPrefix = trimmed
	}
	return bridgev2.BridgeName{
		DisplayName:          "Codex",
		NetworkURL:           "https://github.com/openai/codex",
		NetworkIcon:          id.ContentURIString(""),
		NetworkID:            "codex",
		BeeperBridgeType:     "codex",
		DefaultPort:          29346,
		DefaultCommandPrefix: defaultCommandPrefix,
	}
}

func (cc *CodexConnector) GetConfig() (string, any, configupgrade.Upgrader) {
	return exampleNetworkConfig, &cc.Config, configupgrade.SimpleUpgrader(upgradeConfig)
}

func (cc *CodexConnector) GetDBMetaTypes() database.MetaTypes {
	return database.MetaTypes{
		Portal:    func() any { return &PortalMetadata{} },
		Message:   func() any { return &MessageMetadata{} },
		UserLogin: func() any { return &UserLoginMetadata{} },
		Ghost:     func() any { return &GhostMetadata{} },
	}
}

func (cc *CodexConnector) GetCapabilities() *bridgev2.NetworkGeneralCapabilities {
	return &bridgev2.NetworkGeneralCapabilities{
		Provisioning: bridgev2.ProvisioningCapabilities{
			ResolveIdentifier: bridgev2.ResolveIdentifierCapabilities{
				CreateDM:       true,
				LookupUsername: true,
				ContactList:    true,
			},
		},
	}
}

func (cc *CodexConnector) LoadUserLogin(_ context.Context, login *bridgev2.UserLogin) error {
	return sdk.LoadUserLogin(login, sdk.LoadUserLoginConfig[*CodexClient]{
		Mu:         &cc.clientsMu,
		Clients:    cc.clients,
		ClientsRef: &cc.clients,
		BridgeName: "codex",
		MakeBroken: func(login *bridgev2.UserLogin, reason string) *sdk.BrokenLoginClient {
			return newBrokenLoginClient(login, cc, reason)
		},
		Accept: func(login *bridgev2.UserLogin) (bool, string) {
			if !strings.EqualFold(strings.TrimSpace(loginMetadata(login).Provider), ProviderCodex) {
				return false, "This bridge only supports Codex logins."
			}
			if !cc.codexEnabled() {
				return false, "Codex integration is disabled in the configuration."
			}
			return true, ""
		},
		Create: func(login *bridgev2.UserLogin) (*CodexClient, error) {
			return newCodexClient(login, cc)
		},
		Update: func(client *CodexClient, login *bridgev2.UserLogin) {
			client.SetUserLogin(login)
		},
		AfterLoad: func(client *CodexClient) {
			client.scheduleBootstrapOnce()
		},
	})
}

func (cc *CodexConnector) GetLoginFlows() []bridgev2.LoginFlow {
	return codexLoginFlows()
}

func (cc *CodexConnector) CreateLogin(ctx context.Context, user *bridgev2.User, flowID string) (bridgev2.LoginProcess, error) {
	if !cc.codexEnabled() {
		return nil, sdk.NewLoginRespError(http.StatusForbidden, "Codex login is disabled in the configuration.", "CODEX", "LOGIN_DISABLED")
	}
	if !slices.ContainsFunc(codexLoginFlows(), func(f bridgev2.LoginFlow) bool { return f.ID == flowID }) {
		return nil, bridgev2.ErrInvalidLoginFlowID
	}
	return &CodexLogin{User: user, Connector: cc, FlowID: flowID}, nil
}

func (cc *CodexConnector) GetBridgeInfoVersion() (info, capabilities int) {
	return sdk.DefaultBridgeInfoVersion()
}

func (cc *CodexConnector) FillPortalBridgeInfo(portal *bridgev2.Portal, content *event.BridgeEventContent) {
	if portal == nil {
		return
	}
	sdk.ApplyAgentRemoteBridgeInfo(content, "ai-codex", portal.RoomType)
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
	if cc.Config.Bridge.CommandPrefix == "" {
		cc.Config.Bridge.CommandPrefix = "!codex"
	}
	if cc.Config.Codex == nil {
		cc.Config.Codex = &CodexConfig{}
	}
	if cc.Config.Codex.Enabled == nil {
		enabled := true
		cc.Config.Codex.Enabled = &enabled
	}
	if strings.TrimSpace(cc.Config.Codex.Command) == "" {
		cc.Config.Codex.Command = "codex"
	}
	if strings.TrimSpace(cc.Config.Codex.DefaultModel) == "" {
		cc.Config.Codex.DefaultModel = "gpt-5.1-codex"
	}
	if cc.Config.Codex.NetworkAccess == nil {
		networkAccess := true
		cc.Config.Codex.NetworkAccess = &networkAccess
	}
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
