package openclaw

import (
	"context"
	"strings"
	"sync"

	"go.mau.fi/util/configupgrade"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"

	"github.com/beeper/agentremote"
	bridgesdk "github.com/beeper/agentremote/sdk"
)

var (
	_ bridgev2.NetworkConnector               = (*OpenClawConnector)(nil)
	_ bridgev2.PortalBridgeInfoFillingNetwork = (*OpenClawConnector)(nil)
)

type OpenClawConnector struct {
	*agentremote.ConnectorBase
	br        *bridgev2.Bridge
	Config    Config
	sdkConfig *bridgesdk.Config

	clientsMu sync.Mutex
	clients   map[networkid.UserLoginID]bridgev2.NetworkAPI
}

func NewConnector() *OpenClawConnector {
	oc := &OpenClawConnector{}
	oc.sdkConfig = bridgesdk.NewStandardConnectorConfig(bridgesdk.StandardConnectorConfigParams{
		Name:             "openclaw",
		Description:      "A Matrix↔OpenClaw bridge built on mautrix-go bridgev2.",
		ProtocolID:       "ai-openclaw",
		ProviderIdentity: bridgesdk.ProviderIdentity{IDPrefix: "openclaw", LogKey: "openclaw_msg_id", StatusNetwork: "openclaw"},
		ClientCacheMu:    &oc.clientsMu,
		ClientCache:      &oc.clients,
		InitConnector: func(bridge *bridgev2.Bridge) {
			oc.br = bridge
		},
		StartConnector: func(_ context.Context, _ *bridgev2.Bridge) error {
			bridgesdk.ApplyDefaultCommandPrefix(&oc.Config.Bridge.CommandPrefix, "!openclaw")
			bridgesdk.ApplyBoolDefault(&oc.Config.OpenClaw.Enabled, true)
			return nil
		},
		DisplayName:      "OpenClaw Bridge",
		NetworkURL:       "https://github.com/openclaw/openclaw",
		NetworkID:        "openclaw",
		BeeperBridgeType: "openclaw",
		DefaultPort:      29348,
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
		NetworkCapabilities: func() *bridgev2.NetworkGeneralCapabilities {
			caps := agentremote.DefaultNetworkCapabilities()
			caps.DisappearingMessages = false
			return caps
		},
		AcceptLogin: func(login *bridgev2.UserLogin) (bool, string) {
			meta := loginMetadata(login)
			return strings.EqualFold(strings.TrimSpace(meta.Provider), ProviderOpenClaw), "This bridge only supports OpenClaw logins."
		},
		CreateClient: func(login *bridgev2.UserLogin) (bridgev2.NetworkAPI, error) {
			return newOpenClawClient(login, oc)
		},
		UpdateClient: func(client bridgev2.NetworkAPI, login *bridgev2.UserLogin) {
			if c, ok := client.(*OpenClawClient); ok {
				c.SetUserLogin(login)
			}
		},
		LoginFlows: agentremote.SingleLoginFlow(oc.openClawEnabled(), bridgev2.LoginFlow{
			ID:          ProviderOpenClaw,
			Name:        "OpenClaw",
			Description: "Create a login for an OpenClaw gateway.",
		}),
		CreateLogin: func(_ context.Context, user *bridgev2.User, flowID string) (bridgev2.LoginProcess, error) {
			if err := agentremote.ValidateSingleLoginFlow(flowID, ProviderOpenClaw, oc.openClawEnabled()); err != nil {
				return nil, err
			}
			return &OpenClawLogin{User: user, Connector: oc}, nil
		},
	})
	oc.ConnectorBase = bridgesdk.NewConnectorBase(oc.sdkConfig)
	return oc
}

func (oc *OpenClawConnector) openClawEnabled() bool {
	return oc.Config.OpenClaw.Enabled == nil || *oc.Config.OpenClaw.Enabled
}
