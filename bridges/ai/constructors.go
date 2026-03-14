package ai

import (
	"context"

	"go.mau.fi/util/configupgrade"
	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/commands"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/agentremote"
	"github.com/beeper/agentremote/pkg/aidb"
	bridgesdk "github.com/beeper/agentremote/sdk"
)

func NewAIConnector() *OpenAIConnector {
	oc := &OpenAIConnector{}
	oc.ConnectorBase = agentremote.NewConnector(agentremote.ConnectorSpec{
		Init: func(bridge *bridgev2.Bridge) {
			bridgev2.PortalEventBuffer = 0
			oc.br = bridge
			oc.db = nil
			if bridge != nil && bridge.DB != nil && bridge.DB.Database != nil {
				oc.db = aidb.NewChild(
					bridge.DB.Database,
					dbutil.ZeroLogger(bridge.Log.With().Str("db_section", "ai_bridge").Logger()),
				)
			}
			agentremote.EnsureClientMap(&oc.clientsMu, &oc.clients)
		},
		Start: func(ctx context.Context) error {
			db := oc.bridgeDB()
			if err := aidb.Upgrade(ctx, db, "ai_bridge", "ai bridge database not initialized"); err != nil {
				return err
			}
			oc.applyRuntimeDefaults()
			oc.primeUserLoginCache(ctx)
			if _, err := oc.reconcileManagedBeeperLogin(ctx); err != nil {
				return err
			}
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
		Stop: func(context.Context) {
			agentremote.StopClients(&oc.clientsMu, &oc.clients)
		},
		Name: func() bridgev2.BridgeName {
			return bridgev2.BridgeName{
				DisplayName:          "Beeper Cloud",
				NetworkURL:           "https://www.beeper.com/ai",
				NetworkIcon:          "mxc://beeper.com/51a668657dd9e0132cc823ad9402c6c2d0fc3321",
				NetworkID:            "ai",
				BeeperBridgeType:     "ai",
				DefaultPort:          29345,
				DefaultCommandPrefix: oc.Config.Bridge.CommandPrefix,
			}
		},
		Config: func() (example string, data any, upgrader configupgrade.Upgrader) {
			return exampleNetworkConfig, &oc.Config, configupgrade.SimpleUpgrader(upgradeConfig)
		},
		DBMeta: func() database.MetaTypes {
			return bridgesdk.BuildStandardMetaTypes(
				func() any { return &PortalMetadata{} },
				func() any { return &MessageMetadata{} },
				func() any { return &UserLoginMetadata{} },
				func() any { return &GhostMetadata{} },
			)
		},
		BridgeInfoVersion: func() (info, capabilities int) {
			return agentremote.DefaultBridgeInfoVersion()
		},
		FillBridgeInfo: func(portal *bridgev2.Portal, content *event.BridgeEventContent) {
			applyAIBridgeInfo(portal, portalMeta(portal), content)
		},
		LoadLogin: func(_ context.Context, login *bridgev2.UserLogin) error {
			meta := loginMetadata(login)
			return oc.loadAIUserLogin(login, meta)
		},
		LoginFlows: func() []bridgev2.LoginFlow {
			return oc.getLoginFlows()
		},
		CreateLogin: func(ctx context.Context, user *bridgev2.User, flowID string) (bridgev2.LoginProcess, error) {
			return oc.createLogin(ctx, user, flowID)
		},
	})
	return oc
}
