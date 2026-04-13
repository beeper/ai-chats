package openclaw

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.mau.fi/util/configupgrade"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/sdk"
)

var (
	_ bridgev2.NetworkConnector               = (*OpenClawConnector)(nil)
	_ bridgev2.PortalBridgeInfoFillingNetwork = (*OpenClawConnector)(nil)
)

type OpenClawConnector struct {
	*sdk.ConnectorBase
	br        *bridgev2.Bridge
	Config    Config
	sdkConfig *sdk.Config[*OpenClawClient, *Config]

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
	oc.sdkConfig = sdk.NewStandardConnectorConfig(sdk.StandardConnectorConfigParams[*OpenClawClient, *Config, *PortalMetadata, *MessageMetadata, *UserLoginMetadata, *GhostMetadata]{
		Name:             "openclaw",
		Description:      "OpenClaw Gateway bridge built with the AgentRemote SDK.",
		ProtocolID:       "ai-openclaw",
		ProviderIdentity: sdk.ProviderIdentity{IDPrefix: "openclaw", LogKey: "openclaw_msg_id", StatusNetwork: "openclaw"},
		ClientCacheMu:    &oc.clientsMu,
		ClientCache:      &oc.clients,
		InitConnector: func(bridge *bridgev2.Bridge) {
			oc.br = bridge
		},
		StartConnector: func(_ context.Context, _ *bridgev2.Bridge) error {
			sdk.ApplyDefaultCommandPrefix(&oc.Config.Bridge.CommandPrefix, "!openclaw")
			sdk.ApplyBoolDefault(&oc.Config.OpenClaw.Enabled, true)
			sdk.ApplyBoolDefault(&oc.Config.OpenClaw.Discovery.Enabled, true)
			if oc.Config.OpenClaw.Discovery.TimeoutMS <= 0 {
				oc.Config.OpenClaw.Discovery.TimeoutMS = 2000
			}
			if oc.Config.OpenClaw.Discovery.PrefillTTLSeconds <= 0 {
				oc.Config.OpenClaw.Discovery.PrefillTTLSeconds = 300
			}
			oc.initProvisioning()
			return nil
		},
		DisplayName:      "OpenClaw Gateway",
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
			return sdk.AcceptProviderLogin(login, ProviderOpenClaw, "This bridge only supports OpenClaw logins.", oc.openClawEnabled, "OpenClaw integration is disabled in the configuration.", func(login *bridgev2.UserLogin) string {
				return loginMetadata(login).Provider
			})
		},
		CreateClient: sdk.TypedClientCreator(func(login *bridgev2.UserLogin) (*OpenClawClient, error) {
			return newOpenClawClient(login, oc)
		}),
		UpdateClient: sdk.TypedClientUpdater[*OpenClawClient](),
		LoginFlows: sdk.SingleLoginFlow(oc.openClawEnabled(), bridgev2.LoginFlow{
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
	oc.ConnectorBase = sdk.NewConnectorBase(oc.sdkConfig)
	return oc
}

func (oc *OpenClawConnector) openClawEnabled() bool {
	return oc.Config.OpenClaw.Enabled == nil || *oc.Config.OpenClaw.Enabled
}

const openClawPrefillFlowPrefix = "openclaw_prefill:"

func (oc *OpenClawConnector) loginPrefillTTL() time.Duration {
	if oc == nil {
		return 5 * time.Minute
	}
	seconds := oc.Config.OpenClaw.Discovery.PrefillTTLSeconds
	if seconds <= 0 {
		seconds = 300
	}
	return time.Duration(seconds) * time.Second
}

func (oc *OpenClawConnector) registerLoginPrefill(user *bridgev2.User, url, label string) (string, time.Time) {
	if oc == nil || user == nil {
		return "", time.Time{}
	}
	now := time.Now()
	expiresAt := now.Add(oc.loginPrefillTTL())
	entry := openClawLoginPrefill{
		UserMXID:  user.MXID,
		URL:       strings.TrimSpace(url),
		Label:     strings.TrimSpace(label),
		ExpiresAt: expiresAt,
	}
	id := openClawPrefillFlowPrefix + uuid.NewString()
	oc.prefillsMu.Lock()
	oc.pruneLoginPrefillsLocked(now)
	if oc.prefills == nil {
		oc.prefills = make(map[string]openClawLoginPrefill)
	}
	oc.prefills[id] = entry
	oc.prefillsMu.Unlock()
	return id, expiresAt
}

func (oc *OpenClawConnector) loginPrefill(flowID string, user *bridgev2.User) (openClawLoginPrefill, bool) {
	if oc == nil || user == nil || !strings.HasPrefix(flowID, openClawPrefillFlowPrefix) {
		return openClawLoginPrefill{}, false
	}
	now := time.Now()
	oc.prefillsMu.Lock()
	defer oc.prefillsMu.Unlock()
	oc.pruneLoginPrefillsLocked(now)
	prefill, ok := oc.prefills[flowID]
	if !ok || prefill.UserMXID != user.MXID {
		return openClawLoginPrefill{}, false
	}
	return prefill, true
}

func (oc *OpenClawConnector) pruneLoginPrefillsLocked(now time.Time) {
	if oc == nil || len(oc.prefills) == 0 {
		return
	}
	for id, prefill := range oc.prefills {
		if !prefill.ExpiresAt.IsZero() && !prefill.ExpiresAt.After(now) {
			delete(oc.prefills, id)
		}
	}
}
