package ai

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/commands"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/pkg/aidb"
	"github.com/beeper/agentremote/sdk"
)

func NewAIConnector() *OpenAIConnector {
	oc := &OpenAIConnector{
		clients: make(map[networkid.UserLoginID]bridgev2.NetworkAPI),
	}
	oc.sdkConfig = &sdk.Config[*AIClient, *Config]{
		Name:          "ai",
		Description:   "AI Chats bridge for Beeper",
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
					dbutil.ZeroLogger(bridge.Log.With().Str("db_section", "ai").Logger()),
				)
			}
		},
		StartConnector: func(ctx context.Context, _ *bridgev2.Bridge) error {
			db := oc.bridgeDB()
			if err := aidb.EnsureSchema(ctx, db); err != nil {
				return err
			}
			oc.applyRuntimeDefaults()
			if proc, ok := oc.br.Commands.(*commands.Processor); ok {
				registerCommandsWithOwnerGuard(proc, &oc.Config, &oc.br.Log, HelpSectionAI)
				oc.br.Log.Info().Msg("Registered AI commands with command processor")
			} else {
				oc.br.Log.Warn().Type("commands_type", oc.br.Commands).Msg("Failed to register AI commands: command processor type assertion failed")
			}
			oc.initProvisioning()
			return nil
		},
		BridgeName: func() bridgev2.BridgeName {
			defaultCommandPrefix := "!ai"
			if trimmed := strings.TrimSpace(oc.Config.Bridge.CommandPrefix); trimmed != "" {
				defaultCommandPrefix = trimmed
			}
			return bridgev2.BridgeName{
				DisplayName:          "Beeper AI",
				NetworkURL:           "https://www.beeper.com/ai",
				NetworkIcon:          id.ContentURIString("mxc://beeper.com/51a668657dd9e0132cc823ad9402c6c2d0fc3321"),
				NetworkID:            "ai",
				BeeperBridgeType:     "ai",
				DefaultPort:          29345,
				DefaultCommandPrefix: defaultCommandPrefix,
			}
		},
		ExampleConfig: exampleNetworkConfig,
		ConfigData:    &oc.Config,
		DBMeta: func() database.MetaTypes {
			return database.MetaTypes{
				Portal:    func() any { return &PortalMetadata{} },
				Message:   func() any { return &MessageMetadata{} },
				UserLogin: func() any { return &UserLoginMetadata{} },
				Ghost:     func() any { return &GhostMetadata{} },
			}
		},
		NetworkCapabilities: func() *bridgev2.NetworkGeneralCapabilities {
			return &bridgev2.NetworkGeneralCapabilities{
				Provisioning: bridgev2.ProvisioningCapabilities{
					ResolveIdentifier: bridgev2.ResolveIdentifierCapabilities{
						CreateDM:       true,
						LookupUsername: true,
						ContactList:    true,
						Search:         true,
					},
				},
			}
		},
		FillBridgeInfo: func(portal *bridgev2.Portal, content *event.BridgeEventContent) {
			applyAIChatsBridgeInfo(portal, portalMeta(portal), content)
		},
		LoadLogin: func(ctx context.Context, login *bridgev2.UserLogin) error {
			return oc.loadAIUserLogin(ctx, login, loginMetadata(login), nil)
		},
		GetLoginFlows: oc.getLoginFlows,
		CreateLogin: func(ctx context.Context, user *bridgev2.User, flowID string) (bridgev2.LoginProcess, error) {
			flows := oc.getLoginFlows()
			if !slices.ContainsFunc(flows, func(f bridgev2.LoginFlow) bool { return f.ID == flowID }) {
				return nil, fmt.Errorf("login flow %s is not available", flowID)
			}
			return &OpenAILogin{User: user, Connector: oc, FlowID: flowID}, nil
		},
	}
	oc.ConnectorBase = sdk.NewConnectorBase(oc.sdkConfig)
	return oc
}
