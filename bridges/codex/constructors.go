package codex

import (
	"context"
	"fmt"
	"slices"

	"go.mau.fi/util/configupgrade"
	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/agentremote/pkg/aidb"
	"github.com/beeper/agentremote/sdk"
)

func NewConnector() *CodexConnector {
	cc := &CodexConnector{}
	loginFlows := []bridgev2.LoginFlow{
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
	cc.sdkConfig = sdk.NewStandardConnectorConfig(sdk.StandardConnectorConfigParams[*CodexClient, *Config, *PortalMetadata, *MessageMetadata, *UserLoginMetadata, *GhostMetadata]{
		Name:             "codex",
		Description:      "Codex bridge built with the AgentRemote SDK.",
		ProtocolID:       "ai-codex",
		ProviderIdentity: sdk.ProviderIdentity{IDPrefix: "codex", LogKey: "codex_msg_id", StatusNetwork: "codex"},
		ClientCacheMu:    &cc.clientsMu,
		ClientCache:      &cc.clients,
		InitConnector: func(bridge *bridgev2.Bridge) {
			cc.br = bridge
			if bridge != nil && bridge.DB != nil && bridge.DB.Database != nil {
				cc.db = aidb.NewChild(
					bridge.DB.Database,
					dbutil.ZeroLogger(bridge.Log.With().Str("db_section", "codex_bridge").Logger()),
				)
			}
		},
		StartConnector: func(ctx context.Context, _ *bridgev2.Bridge) error {
			db := cc.bridgeDB()
			if db == nil {
				return fmt.Errorf("codex database not initialized")
			}
			if err := aidb.EnsureSchema(ctx, db); err != nil {
				return err
			}
			cc.applyRuntimeDefaults()
			sdk.PrimeUserLoginCache(ctx, cc.br)
			return nil
		},
		DisplayName:      "Codex",
		NetworkURL:       "https://github.com/openai/codex",
		NetworkID:        "codex",
		BeeperBridgeType: "codex",
		DefaultPort:      29346,
		DefaultCommandPrefix: func() string {
			return sdk.ResolveCommandPrefix(cc.Config.Bridge.CommandPrefix, "!ai")
		},
		FillBridgeInfo: func(portal *bridgev2.Portal, content *event.BridgeEventContent) {
			if portal == nil {
				return
			}
			sdk.ApplyAgentRemoteBridgeInfo(content, "ai-codex", portal.RoomType, sdk.AIRoomKindAgent)
		},
		ExampleConfig:  exampleNetworkConfig,
		ConfigData:     &cc.Config,
		ConfigUpgrader: configupgrade.SimpleUpgrader(upgradeConfig),
		NewPortal:      func() *PortalMetadata { return &PortalMetadata{} },
		NewMessage:     func() *MessageMetadata { return &MessageMetadata{} },
		NewLogin:       func() *UserLoginMetadata { return &UserLoginMetadata{} },
		NewGhost:       func() *GhostMetadata { return &GhostMetadata{} },
		NetworkCapabilities: func() *bridgev2.NetworkGeneralCapabilities {
			return &bridgev2.NetworkGeneralCapabilities{
				Provisioning: bridgev2.ProvisioningCapabilities{
					ResolveIdentifier: bridgev2.ResolveIdentifierCapabilities{
						CreateDM:       true,
						LookupUsername: true,
						ContactList:    true,
					},
				},
			}
		},
		AcceptLogin: func(login *bridgev2.UserLogin) (bool, string) {
			return sdk.AcceptProviderLogin(login, ProviderCodex, "This bridge only supports Codex logins.", cc.codexEnabled, "Codex integration is disabled in the configuration.", func(login *bridgev2.UserLogin) string {
				return loginMetadata(login).Provider
			})
		},
		MakeBrokenLogin: func(l *bridgev2.UserLogin, reason string) *sdk.BrokenLoginClient {
			return newBrokenLoginClient(l, cc, reason)
		},
		CreateClient: sdk.TypedClientCreator(func(login *bridgev2.UserLogin) (*CodexClient, error) { return newCodexClient(login, cc) }),
		UpdateClient: sdk.TypedClientUpdater[*CodexClient](),
		AfterLoadClient: func(client bridgev2.NetworkAPI) {
			if c, ok := client.(*CodexClient); ok {
				c.scheduleBootstrapOnce()
			}
		},
		LoginFlows: loginFlows,
		CreateLogin: func(ctx context.Context, user *bridgev2.User, flowID string) (bridgev2.LoginProcess, error) {
			if !cc.codexEnabled() {
				return nil, sdk.NewLoginRespError(403, "Codex login is disabled in the configuration.", "CODEX", "LOGIN_DISABLED")
			}
			if !slices.ContainsFunc(loginFlows, func(f bridgev2.LoginFlow) bool { return f.ID == flowID }) {
				return nil, bridgev2.ErrInvalidLoginFlowID
			}
			return &CodexLogin{User: user, Connector: cc, FlowID: flowID}, nil
		},
	})
	cc.sdkConfig.Agent = codexSDKAgent()
	cc.ConnectorBase = sdk.NewConnectorBase(cc.sdkConfig)
	return cc
}

func codexSDKAgent() *sdk.Agent {
	return &sdk.Agent{
		ID:           string(codexGhostID),
		Name:         "Codex",
		Description:  "Codex agent",
		Identifiers:  []string{"codex"},
		ModelKey:     "codex",
		Capabilities: sdk.BaseAgentCapabilities(),
	}
}

func newBrokenLoginClient(login *bridgev2.UserLogin, connector *CodexConnector, reason string) *sdk.BrokenLoginClient {
	c := sdk.NewBrokenLoginClient(login, reason)
	c.OnLogout = func(ctx context.Context, login *bridgev2.UserLogin) {
		tmp := &CodexClient{UserLogin: login, connector: connector}
		tmp.purgeCodexHomeBestEffort(ctx)
		tmp.purgeCodexCwdsBestEffort(ctx)
		if connector != nil && login != nil {
			sdk.RemoveClientFromCache(&connector.clientsMu, connector.clients, login.ID)
		}
	}
	return c
}
