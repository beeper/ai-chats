package opencode

import (
	"context"
	"slices"
	"sync"

	"go.mau.fi/util/configupgrade"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"

	"github.com/beeper/agentremote/sdk"
)

var (
	_ bridgev2.NetworkConnector               = (*OpenCodeConnector)(nil)
	_ bridgev2.PortalBridgeInfoFillingNetwork = (*OpenCodeConnector)(nil)
)

type OpenCodeConnector struct {
	*sdk.ConnectorBase
	br        *bridgev2.Bridge
	Config    Config
	sdkConfig *sdk.Config[*OpenCodeClient, *Config]

	clientsMu sync.Mutex
	clients   map[networkid.UserLoginID]bridgev2.NetworkAPI
}

func NewConnector() *OpenCodeConnector {
	oc := &OpenCodeConnector{}
	loginFlows := []bridgev2.LoginFlow{
		{
			ID:          FlowOpenCodeRemote,
			Name:        "Remote OpenCode",
			Description: "Connect to an already running OpenCode server.",
		},
		{
			ID:          FlowOpenCodeManaged,
			Name:        "Managed OpenCode",
			Description: "Let the bridge spawn and manage OpenCode processes for you.",
		},
	}
	oc.sdkConfig = sdk.NewStandardConnectorConfig(sdk.StandardConnectorConfigParams[*OpenCodeClient, *Config, *PortalMetadata, *MessageMetadata, *UserLoginMetadata, *GhostMetadata]{
		Name:             "opencode",
		Description:      "OpenCode bridge built with the AgentRemote SDK.",
		ProtocolID:       "ai-opencode",
		AgentCatalog:     openCodeAgentCatalog{},
		ProviderIdentity: sdk.ProviderIdentity{IDPrefix: "opencode", LogKey: "opencode_msg_id", StatusNetwork: "opencode"},
		ClientCacheMu:    &oc.clientsMu,
		ClientCache:      &oc.clients,
		InitConnector: func(bridge *bridgev2.Bridge) {
			oc.br = bridge
		},
		StartConnector: func(_ context.Context, _ *bridgev2.Bridge) error {
			sdk.ApplyDefaultCommandPrefix(&oc.Config.Bridge.CommandPrefix, "!opencode")
			sdk.ApplyBoolDefault(&oc.Config.OpenCode.Enabled, true)
			return nil
		},
		DisplayName:      "OpenCode",
		NetworkURL:       "https://api.ai",
		NetworkID:        "opencode",
		BeeperBridgeType: "opencode",
		DefaultPort:      29347,
		DefaultCommandPrefix: func() string {
			return oc.Config.Bridge.CommandPrefix
		},
		ExampleConfig:  exampleNetworkConfig,
		ConfigData:     &oc.Config,
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
						Search:         true,
					},
				},
			}
		},
		AcceptLogin: func(login *bridgev2.UserLogin) (bool, string) {
			return sdk.AcceptProviderLogin(login, ProviderOpenCode, "This bridge only supports OpenCode logins.", oc.openCodeEnabled, "OpenCode integration is disabled in the configuration.", func(login *bridgev2.UserLogin) string {
				return loginMetadata(login).Provider
			})
		},
		CreateClient: sdk.TypedClientCreator(func(login *bridgev2.UserLogin) (*OpenCodeClient, error) { return newOpenCodeClient(login, oc) }),
		UpdateClient: sdk.TypedClientUpdater[*OpenCodeClient](),
		LoginFlows:   loginFlows,
		CreateLogin: func(_ context.Context, user *bridgev2.User, flowID string) (bridgev2.LoginProcess, error) {
			if err := sdk.ValidateLoginFlow(flowID, oc.openCodeEnabled(), "OpenCode login is disabled in the configuration.", "OPENCODE", "LOGIN_DISABLED", func(flowID string) bool {
				return slices.ContainsFunc(loginFlows, func(f bridgev2.LoginFlow) bool { return f.ID == flowID })
			}); err != nil {
				return nil, err
			}
			return &OpenCodeLogin{User: user, Connector: oc, FlowID: flowID}, nil
		},
	})
	oc.ConnectorBase = sdk.NewConnectorBase(oc.sdkConfig)
	return oc
}

func (oc *OpenCodeConnector) openCodeEnabled() bool {
	return oc.Config.OpenCode.Enabled == nil || *oc.Config.OpenCode.Enabled
}
