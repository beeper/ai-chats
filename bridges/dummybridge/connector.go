package dummybridge

import (
	"context"
	"net/http"
	"strings"
	"sync"

	"go.mau.fi/util/configupgrade"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/sdk"
)

var (
	_ bridgev2.NetworkConnector               = (*DummyBridgeConnector)(nil)
	_ bridgev2.PortalBridgeInfoFillingNetwork = (*DummyBridgeConnector)(nil)
)

type DummyBridgeConnector struct {
	*sdk.ConnectorBase
	br        *bridgev2.Bridge
	Config    Config
	sdkConfig *sdk.Config[*dummySession, *Config]

	clientsMu sync.Mutex
	clients   map[networkid.UserLoginID]bridgev2.NetworkAPI

	chatMu sync.Mutex
}

func NewConnector() *DummyBridgeConnector {
	dc := &DummyBridgeConnector{}
	dc.sdkConfig = &sdk.Config[*dummySession, *Config]{
		Name:             "dummybridge",
		Description:      "DummyBridge demo bridge built with the AgentRemote SDK.",
		ProtocolID:       "ai-dummybridge",
		ProviderIdentity: sdk.ProviderIdentity{IDPrefix: "dummybridge", LogKey: "dummybridge_msg_id", StatusNetwork: "dummybridge"},
		ClientCacheMu:    &dc.clientsMu,
		ClientCache:      &dc.clients,
		InitConnector: func(bridge *bridgev2.Bridge) {
			dc.br = bridge
		},
		StartConnector: func(_ context.Context, _ *bridgev2.Bridge) error {
			if dc.Config.Bridge.CommandPrefix == "" {
				dc.Config.Bridge.CommandPrefix = "!dummybridge"
			}
			if dc.Config.DummyBridge.Enabled == nil {
				enabled := true
				dc.Config.DummyBridge.Enabled = &enabled
			}
			return nil
		},
		BridgeName: func() bridgev2.BridgeName {
			defaultCommandPrefix := "!dummybridge"
			if trimmed := strings.TrimSpace(dc.Config.Bridge.CommandPrefix); trimmed != "" {
				defaultCommandPrefix = trimmed
			}
			return bridgev2.BridgeName{
				DisplayName:          "DummyBridge",
				NetworkURL:           "https://github.com/beeper/agentremote",
				NetworkIcon:          id.ContentURIString(""),
				NetworkID:            "dummybridge",
				BeeperBridgeType:     "dummybridge",
				DefaultPort:          29349,
				DefaultCommandPrefix: defaultCommandPrefix,
			}
		},
		ExampleConfig:  exampleNetworkConfig,
		ConfigData:     &dc.Config,
		ConfigUpgrader: configupgrade.SimpleUpgrader(upgradeConfig),
		DBMeta: func() database.MetaTypes {
			return database.MetaTypes{
				Portal:    func() any { return &PortalMetadata{} },
				Message:   func() any { return &MessageMetadata{} },
				UserLogin: func() any { return &UserLoginMetadata{} },
				Ghost:     func() any { return &GhostMetadata{} },
			}
		},
		AcceptLogin: func(login *bridgev2.UserLogin) (bool, string) {
			if !strings.EqualFold(strings.TrimSpace(loginMetadata(login).Provider), ProviderDummyBridge) {
				return false, "This bridge only supports DummyBridge logins."
			}
			if !dc.enabled() {
				return false, "DummyBridge integration is disabled in the configuration."
			}
			return true, ""
		},
		LoginFlows: func() []bridgev2.LoginFlow {
			if !dc.enabled() {
				return nil
			}
			return []bridgev2.LoginFlow{{
				ID:          ProviderDummyBridge,
				Name:        "DummyBridge",
				Description: "Create a synthetic demo login for turn and streaming tests.",
			}}
		}(),
		CreateLogin: func(_ context.Context, user *bridgev2.User, flowID string) (bridgev2.LoginProcess, error) {
			if flowID != ProviderDummyBridge {
				return nil, bridgev2.ErrInvalidLoginFlowID
			}
			if !dc.enabled() {
				return nil, sdk.NewLoginRespError(http.StatusForbidden, "This login flow is disabled.", "LOGIN", "DISABLED")
			}
			return &DummyBridgeLogin{User: user, Connector: dc}, nil
		},
	}
	dc.sdkConfig.Agent = dummySDKAgent()
	dc.sdkConfig.OnConnect = dc.onConnect
	dc.sdkConfig.OnDisconnect = dc.onDisconnect
	dc.sdkConfig.OnMessage = dc.onMessage
	dc.sdkConfig.GetChatInfo = dc.getChatInfo
	dc.sdkConfig.GetUserInfo = dc.getUserInfo
	dc.ConnectorBase = sdk.NewConnectorBase(dc.sdkConfig)
	return dc
}

func (dc *DummyBridgeConnector) enabled() bool {
	return dc.Config.DummyBridge.Enabled == nil || *dc.Config.DummyBridge.Enabled
}
