package codex

import (
	"context"
	"slices"

	"go.mau.fi/util/configupgrade"
	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/agentremote"
	"github.com/beeper/agentremote/pkg/aidb"
	bridgesdk "github.com/beeper/agentremote/sdk"
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
	cc.sdkConfig = bridgesdk.NewStandardConnectorConfig(bridgesdk.StandardConnectorConfigParams{
		Name:             "codex",
		Description:      "A Matrix↔Codex bridge built on mautrix-go bridgev2.",
		ProtocolID:       "ai-codex",
		ProviderIdentity: bridgesdk.ProviderIdentity{IDPrefix: "codex", LogKey: "codex_msg_id", StatusNetwork: "codex"},
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
			if err := aidb.Upgrade(ctx, db, "codex_bridge", "codex bridge database not initialized"); err != nil {
				return err
			}
			cc.applyRuntimeDefaults()
			agentremote.PrimeUserLoginCache(ctx, cc.br)
			cc.reconcileHostAuthLogins(ctx)
			return nil
		},
		DisplayName:      "Codex Bridge",
		NetworkURL:       "https://github.com/openai/codex",
		NetworkID:        "codex",
		BeeperBridgeType: "codex",
		DefaultPort:      29346,
		DefaultCommandPrefix: func() string {
			return cc.Config.Bridge.CommandPrefix
		},
		FillBridgeInfo: func(portal *bridgev2.Portal, content *event.BridgeEventContent) {
			if portal == nil {
				return
			}
			agentremote.ApplyAgentRemoteBridgeInfo(content, "ai-codex", portal.RoomType, agentremote.AIRoomKindAgent)
		},
		ExampleConfig:  exampleNetworkConfig,
		ConfigData:     &cc.Config,
		ConfigUpgrader: configupgrade.SimpleUpgrader(upgradeConfig),
		NewPortal:      func() any { return &PortalMetadata{} },
		NewMessage:     func() any { return &MessageMetadata{} },
		NewLogin:       func() any { return &UserLoginMetadata{} },
		NewGhost:       func() any { return &GhostMetadata{} },
		AcceptLogin: func(login *bridgev2.UserLogin) (bool, string) {
			return bridgesdk.AcceptProviderLogin(login, ProviderCodex, "This bridge only supports Codex logins.", cc.codexEnabled, "Codex integration is disabled in the configuration.", func(login *bridgev2.UserLogin) string {
				return loginMetadata(login).Provider
			})
		},
		MakeBrokenLogin: func(l *bridgev2.UserLogin, reason string) *agentremote.BrokenLoginClient {
			return newBrokenLoginClient(l, cc, reason)
		},
		CreateClient: bridgesdk.TypedClientCreator(func(login *bridgev2.UserLogin) (*CodexClient, error) { return newCodexClient(login, cc) }),
		UpdateClient: bridgesdk.TypedClientUpdater[*CodexClient](),
		AfterLoadClient: func(client bridgev2.NetworkAPI) {
			if c, ok := client.(*CodexClient); ok {
				c.scheduleBootstrapOnce()
			}
		},
		LoginFlows: loginFlows,
		CreateLogin: func(ctx context.Context, user *bridgev2.User, flowID string) (bridgev2.LoginProcess, error) {
			if !cc.codexEnabled() {
				return nil, agentremote.NewLoginRespError(403, "Codex login is disabled in the configuration.", "CODEX", "LOGIN_DISABLED")
			}
			if !slices.ContainsFunc(loginFlows, func(f bridgev2.LoginFlow) bool { return f.ID == flowID }) {
				return nil, bridgev2.ErrInvalidLoginFlowID
			}
			if err := cc.ensureHostAuthLoginForUser(ctx, user); err != nil && cc.br != nil {
				cc.br.Log.Debug().Err(err).Stringer("mxid", user.MXID).Msg("Host-auth reconcile: create-login reconcile failed")
			}
			return &CodexLogin{User: user, Connector: cc, FlowID: flowID}, nil
		},
	})
	cc.sdkConfig.Agent = codexSDKAgent()
	cc.ConnectorBase = bridgesdk.NewConnectorBase(cc.sdkConfig)
	return cc
}
