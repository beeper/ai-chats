package ai

import (
	"context"

	"go.mau.fi/util/configupgrade"
	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/commands"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/agentremote/pkg/aidb"
	bridgesdk "github.com/beeper/agentremote/sdk"
)

func NewAIConnector() *OpenAIConnector {
	oc := &OpenAIConnector{
		clients: make(map[networkid.UserLoginID]bridgev2.NetworkAPI),
	}
	oc.sdkConfig = bridgesdk.NewStandardConnectorConfig(bridgesdk.StandardConnectorConfigParams{
		Name:          "ai",
		Description:   "AI Chats for Beeper, built on mautrix-go bridgev2.",
		ProtocolID:    "ai",
		AgentCatalog:  aiAgentCatalog{connector: oc},
		ClientCacheMu: &oc.clientsMu,
		ClientCache:   &oc.clients,
		InitConnector: func(bridge *bridgev2.Bridge) {
			oc.br = bridge
			oc.db = nil
			if bridge != nil && bridge.DB != nil && bridge.DB.Database != nil {
				oc.db = aidb.NewChild(
					bridge.DB.Database,
					dbutil.ZeroLogger(bridge.Log.With().Str("db_section", "agentremote").Logger()),
				)
			}
		},
		StartConnector: func(ctx context.Context, _ *bridgev2.Bridge) error {
			db := oc.bridgeDB()
			if err := aidb.Upgrade(ctx, db, "agentremote", "AgentRemote database not initialized"); err != nil {
				return err
			}
			oc.applyRuntimeDefaults()
			oc.primeUserLoginCache(ctx)
			if proc, ok := oc.br.Commands.(*commands.Processor); ok {
				registerCommandsWithOwnerGuard(proc, &oc.Config, &oc.br.Log, HelpSectionAI)
				oc.br.Log.Info().Msg("Registered AI commands with command processor")
			} else {
				oc.br.Log.Warn().Type("commands_type", oc.br.Commands).Msg("Failed to register AI commands: command processor type assertion failed")
			}
			oc.registerCustomEventHandlers()
			oc.initProvisioning()
			return nil
		},
		DisplayName:      "Beeper Cloud",
		NetworkURL:       "https://www.beeper.com/ai",
		NetworkIcon:      "mxc://beeper.com/51a668657dd9e0132cc823ad9402c6c2d0fc3321",
		NetworkID:        "ai",
		BeeperBridgeType: "ai",
		DefaultPort:      29345,
		DefaultCommandPrefix: func() string {
			return oc.Config.Bridge.CommandPrefix
		},
		ExampleConfig:  exampleNetworkConfig,
		ConfigData:     &oc.Config,
		ConfigUpgrader: configupgrade.SimpleUpgrader(upgradeConfig),
		NewPortal:      func() any { return &PortalMetadata{} },
		NewMessage:     func() any { return &MessageMetadata{} },
		NewLogin:       func() any { return &UserLoginMetadata{} },
		NewGhost:       func() any { return &GhostMetadata{} },
		FillBridgeInfo: func(portal *bridgev2.Portal, content *event.BridgeEventContent) {
			applyAgentRemoteBridgeInfo(portal, portalMeta(portal), content)
		},
		LoadLogin: func(_ context.Context, login *bridgev2.UserLogin) error {
			return oc.loadAIUserLogin(login, loginMetadata(login))
		},
		GetLoginFlows: oc.getLoginFlows,
		CreateLogin: func(ctx context.Context, user *bridgev2.User, flowID string) (bridgev2.LoginProcess, error) {
			return oc.createLogin(ctx, user, flowID)
		},
	})
	oc.ConnectorBase = bridgesdk.NewConnectorBase(oc.sdkConfig)
	return oc
}
