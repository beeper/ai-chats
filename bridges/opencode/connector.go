package opencode

import (
	"context"
	"slices"
	"strings"
	"sync"

	"go.mau.fi/util/configupgrade"
	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"

	"github.com/beeper/agentremote"
	bridgesdk "github.com/beeper/agentremote/sdk"
)

var (
	_ bridgev2.NetworkConnector               = (*OpenCodeConnector)(nil)
	_ bridgev2.PortalBridgeInfoFillingNetwork = (*OpenCodeConnector)(nil)
)

type OpenCodeConnector struct {
	*agentremote.ConnectorBase
	br        *bridgev2.Bridge
	Config    Config
	sdkConfig *bridgesdk.Config

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
	oc.sdkConfig = &bridgesdk.Config{
		Name:             "opencode",
		Description:      "A Matrix↔OpenCode bridge built on mautrix-go bridgev2.",
		ProtocolID:       "ai-opencode",
		AgentCatalog:     openCodeAgentCatalog{},
		ProviderIdentity: bridgesdk.ProviderIdentity{IDPrefix: "opencode", LogKey: "opencode_msg_id", StatusNetwork: "opencode"},
		ClientCacheMu:    &oc.clientsMu,
		ClientCache:      &oc.clients,
		GetCapabilities: func(session any, _ *bridgesdk.Conversation) *bridgesdk.RoomFeatures {
			return &bridgesdk.RoomFeatures{Custom: openCodeMatrixRoomFeatures()}
		},
		InitConnector: func(bridge *bridgev2.Bridge) {
			oc.br = bridge
		},
		StartConnector: func(_ context.Context, _ *bridgev2.Bridge) error {
			if oc.Config.Bridge.CommandPrefix == "" {
				oc.Config.Bridge.CommandPrefix = "!opencode"
			}
			if oc.Config.OpenCode.Enabled == nil {
				oc.Config.OpenCode.Enabled = ptr.Ptr(true)
			}
			return nil
		},
		BridgeName: func() bridgev2.BridgeName {
			return bridgev2.BridgeName{
				DisplayName:          "OpenCode Bridge",
				NetworkURL:           "https://api.ai",
				NetworkID:            "opencode",
				BeeperBridgeType:     "opencode",
				DefaultPort:          29347,
				DefaultCommandPrefix: oc.Config.Bridge.CommandPrefix,
			}
		},
		ExampleConfig:  exampleNetworkConfig,
		ConfigData:     &oc.Config,
		ConfigUpgrader: configupgrade.SimpleUpgrader(upgradeConfig),
		DBMeta: func() database.MetaTypes {
			return agentremote.BuildMetaTypes(
				func() any { return &PortalMetadata{} },
				func() any { return &MessageMetadata{} },
				func() any { return &UserLoginMetadata{} },
				func() any { return &GhostMetadata{} },
			)
		},
		AcceptLogin: func(login *bridgev2.UserLogin) (bool, string) {
			meta := loginMetadata(login)
			if !strings.EqualFold(strings.TrimSpace(meta.Provider), ProviderOpenCode) {
				return false, "This bridge only supports OpenCode logins."
			}
			if !oc.openCodeEnabled() {
				return false, "OpenCode integration is disabled in the configuration."
			}
			return true, ""
		},
		CreateClient: func(l *bridgev2.UserLogin) (bridgev2.NetworkAPI, error) {
			return newOpenCodeClient(l, oc)
		},
		UpdateClient: func(client bridgev2.NetworkAPI, login *bridgev2.UserLogin) {
			if c, ok := client.(*OpenCodeClient); ok {
				c.SetUserLogin(login)
			}
		},
		LoginFlows: loginFlows,
		CreateLogin: func(_ context.Context, user *bridgev2.User, flowID string) (bridgev2.LoginProcess, error) {
			if !oc.openCodeEnabled() {
				return nil, bridgev2.ErrNotLoggedIn
			}
			if !slices.ContainsFunc(loginFlows, func(f bridgev2.LoginFlow) bool { return f.ID == flowID }) {
				return nil, bridgev2.ErrInvalidLoginFlowID
			}
			return &OpenCodeLogin{User: user, Connector: oc, FlowID: flowID}, nil
		},
	}
	oc.ConnectorBase = bridgesdk.NewConnectorBase(oc.sdkConfig)
	return oc
}

func (oc *OpenCodeConnector) openCodeEnabled() bool {
	return oc.Config.OpenCode.Enabled == nil || *oc.Config.OpenCode.Enabled
}
