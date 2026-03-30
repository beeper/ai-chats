package dummybridge

import (
	"context"
	"sync"

	"go.mau.fi/util/configupgrade"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"

	"github.com/beeper/agentremote"
	bridgesdk "github.com/beeper/agentremote/sdk"
)

var (
	_ bridgev2.NetworkConnector               = (*DummyBridgeConnector)(nil)
	_ bridgev2.PortalBridgeInfoFillingNetwork = (*DummyBridgeConnector)(nil)
)

type DummyBridgeConnector struct {
	*agentremote.ConnectorBase
	br        *bridgev2.Bridge
	Config    Config
	sdkConfig *bridgesdk.Config[*dummySession, *Config]

	clientsMu sync.Mutex
	clients   map[networkid.UserLoginID]bridgev2.NetworkAPI

	chatMu sync.Mutex
}

func NewConnector() *DummyBridgeConnector {
	dc := &DummyBridgeConnector{}
	dc.sdkConfig = bridgesdk.NewStandardConnectorConfig(bridgesdk.StandardConnectorConfigParams[*dummySession, *Config, *PortalMetadata, *MessageMetadata, *UserLoginMetadata, *GhostMetadata]{
		Name:             "dummybridge",
		Description:      "A synthetic Matrix↔DummyBridge demo bridge built on the AgentRemote SDK.",
		ProtocolID:       "ai-dummybridge",
		ProviderIdentity: bridgesdk.ProviderIdentity{IDPrefix: "dummybridge", LogKey: "dummybridge_msg_id", StatusNetwork: "dummybridge"},
		ClientCacheMu:    &dc.clientsMu,
		ClientCache:      &dc.clients,
		InitConnector: func(bridge *bridgev2.Bridge) {
			dc.br = bridge
		},
		StartConnector: func(_ context.Context, _ *bridgev2.Bridge) error {
			bridgesdk.ApplyDefaultCommandPrefix(&dc.Config.Bridge.CommandPrefix, "!dummybridge")
			bridgesdk.ApplyBoolDefault(&dc.Config.DummyBridge.Enabled, true)
			return nil
		},
		DisplayName:      "DummyBridge",
		NetworkURL:       "https://github.com/beeper/agentremote",
		NetworkID:        "dummybridge",
		BeeperBridgeType: "dummybridge",
		DefaultPort:      29349,
		DefaultCommandPrefix: func() string {
			return bridgesdk.ResolveCommandPrefix(dc.Config.Bridge.CommandPrefix, "!dummybridge")
		},
		ExampleConfig:  exampleNetworkConfig,
		ConfigData:     &dc.Config,
		ConfigUpgrader: configupgrade.SimpleUpgrader(upgradeConfig),
		NewPortal:      func() *PortalMetadata { return &PortalMetadata{} },
		NewMessage:     func() *MessageMetadata { return &MessageMetadata{} },
		NewLogin:       func() *UserLoginMetadata { return &UserLoginMetadata{} },
		NewGhost:       func() *GhostMetadata { return &GhostMetadata{} },
		AcceptLogin: func(login *bridgev2.UserLogin) (bool, string) {
			return bridgesdk.AcceptProviderLogin(login, ProviderDummyBridge, "This bridge only supports DummyBridge logins.", dc.enabled, "DummyBridge integration is disabled in the configuration.", func(login *bridgev2.UserLogin) string {
				return loginMetadata(login).Provider
			})
		},
		LoginFlows: agentremote.SingleLoginFlow(dc.enabled(), bridgev2.LoginFlow{
			ID:          ProviderDummyBridge,
			Name:        "DummyBridge",
			Description: "Create a synthetic demo login for turn and streaming tests.",
		}),
		CreateLogin: func(_ context.Context, user *bridgev2.User, flowID string) (bridgev2.LoginProcess, error) {
			if err := agentremote.ValidateSingleLoginFlow(flowID, ProviderDummyBridge, dc.enabled()); err != nil {
				return nil, err
			}
			return &DummyBridgeLogin{User: user, Connector: dc}, nil
		},
	})
	dc.sdkConfig.Agent = dummySDKAgent()
	dc.sdkConfig.OnConnect = dc.onConnect
	dc.sdkConfig.OnDisconnect = dc.onDisconnect
	dc.sdkConfig.OnMessage = dc.onMessage
	dc.sdkConfig.GetContactList = dc.getContactList
	dc.sdkConfig.SearchUsers = dc.searchUsers
	dc.sdkConfig.ResolveIdentifier = dc.resolveIdentifier
	dc.sdkConfig.GetChatInfo = dc.getChatInfo
	dc.sdkConfig.GetUserInfo = dc.getUserInfo
	dc.ConnectorBase = bridgesdk.NewConnectorBase(dc.sdkConfig)
	return dc
}

func (dc *DummyBridgeConnector) enabled() bool {
	return dc.Config.DummyBridge.Enabled == nil || *dc.Config.DummyBridge.Enabled
}
