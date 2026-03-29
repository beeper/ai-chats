package openclaw

import (
	"context"
	"sync"
	"time"

	"go.mau.fi/util/configupgrade"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"

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
	sdkConfig *bridgesdk.Config[*OpenClawClient, *Config]

	clientsMu sync.Mutex
	clients   map[networkid.UserLoginID]bridgev2.NetworkAPI

	prefillsMu sync.Mutex
	prefills   map[string]openClawLoginPrefill
}

type openClawLoginPrefill struct {
	UserMXID  id.UserID
	URL       string
	Label     string
	ExpiresAt time.Time
}

func NewConnector() *OpenClawConnector {
	oc := &OpenClawConnector{}
	oc.sdkConfig = bridgesdk.NewStandardConnectorConfig(bridgesdk.StandardConnectorConfigParams[*OpenClawClient, *Config, *PortalMetadata, *MessageMetadata, *UserLoginMetadata, *GhostMetadata]{
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
			bridgesdk.ApplyBoolDefault(&oc.Config.OpenClaw.Discovery.Enabled, true)
			if oc.Config.OpenClaw.Discovery.TimeoutMS <= 0 {
				oc.Config.OpenClaw.Discovery.TimeoutMS = 2000
			}
			if oc.Config.OpenClaw.Discovery.PrefillTTLSeconds <= 0 {
				oc.Config.OpenClaw.Discovery.PrefillTTLSeconds = 300
			}
			oc.initProvisioning()
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
		NewPortal:      func() *PortalMetadata { return &PortalMetadata{} },
		NewMessage:     func() *MessageMetadata { return &MessageMetadata{} },
		NewLogin:       func() *UserLoginMetadata { return &UserLoginMetadata{} },
		NewGhost:       func() *GhostMetadata { return &GhostMetadata{} },
		NetworkCapabilities: func() *bridgev2.NetworkGeneralCapabilities {
			caps := agentremote.DefaultNetworkCapabilities()
			caps.DisappearingMessages = false
			return caps
		},
		AcceptLogin: func(login *bridgev2.UserLogin) (bool, string) {
			return bridgesdk.AcceptProviderLogin(login, ProviderOpenClaw, "This bridge only supports OpenClaw logins.", oc.openClawEnabled, "OpenClaw integration is disabled in the configuration.", func(login *bridgev2.UserLogin) string {
				return loginMetadata(login).Provider
			})
		},
		CreateClient: bridgesdk.TypedClientCreator(func(login *bridgev2.UserLogin) (*OpenClawClient, error) {
			return newOpenClawClient(login, oc)
		}),
		UpdateClient: bridgesdk.TypedClientUpdater[*OpenClawClient](),
		LoginFlows: agentremote.SingleLoginFlow(oc.openClawEnabled(), bridgev2.LoginFlow{
			ID:          ProviderOpenClaw,
			Name:        "OpenClaw",
			Description: "Create a login for an OpenClaw gateway.",
		}),
		CreateLogin: func(_ context.Context, user *bridgev2.User, flowID string) (bridgev2.LoginProcess, error) {
			if !oc.openClawEnabled() {
				return nil, bridgev2.ErrInvalidLoginFlowID
			}
			if flowID == ProviderOpenClaw {
				return &OpenClawLogin{User: user, Connector: oc}, nil
			}
			prefill, ok := oc.loginPrefill(flowID, user)
			if !ok {
				return nil, bridgev2.ErrInvalidLoginFlowID
			}
			return &OpenClawLogin{
				User:         user,
				Connector:    oc,
				prefillURL:   prefill.URL,
				prefillLabel: prefill.Label,
			}, nil
		},
	})
	oc.ConnectorBase = bridgesdk.NewConnectorBase(oc.sdkConfig)
	return oc
}

func (oc *OpenClawConnector) openClawEnabled() bool {
	return oc.Config.OpenClaw.Enabled == nil || *oc.Config.OpenClaw.Enabled
}
