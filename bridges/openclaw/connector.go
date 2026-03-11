package openclaw

import (
	"context"
	"strings"
	"sync"

	"go.mau.fi/util/configupgrade"
	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"

	"github.com/beeper/agentremote/pkg/bridgeadapter"
)

var (
	_ bridgev2.NetworkConnector               = (*OpenClawConnector)(nil)
	_ bridgev2.PortalBridgeInfoFillingNetwork = (*OpenClawConnector)(nil)
)

type OpenClawConnector struct {
	bridgeadapter.BaseConnectorMethods
	br     *bridgev2.Bridge
	Config Config

	clientsMu sync.Mutex
	clients   map[networkid.UserLoginID]bridgev2.NetworkAPI
}

func NewConnector() *OpenClawConnector {
	return &OpenClawConnector{
		BaseConnectorMethods: bridgeadapter.BaseConnectorMethods{ProtocolID: "ai-openclaw"},
	}
}

func (oc *OpenClawConnector) Init(bridge *bridgev2.Bridge) {
	oc.br = bridge
	bridgeadapter.EnsureClientMap(&oc.clientsMu, &oc.clients)
}

func (oc *OpenClawConnector) Start(_ context.Context) error {
	if oc.Config.Bridge.CommandPrefix == "" {
		oc.Config.Bridge.CommandPrefix = "!openclaw"
	}
	if oc.Config.OpenClaw.Enabled == nil {
		oc.Config.OpenClaw.Enabled = ptr.Ptr(true)
	}
	return nil
}

func (oc *OpenClawConnector) Stop(_ context.Context) {
	bridgeadapter.StopClients(&oc.clientsMu, &oc.clients)
}

func (oc *OpenClawConnector) GetCapabilities() *bridgev2.NetworkGeneralCapabilities {
	caps := bridgeadapter.DefaultNetworkCapabilities()
	// OpenClaw supports session reset/delete, but not timer-backed disappearing messages.
	caps.DisappearingMessages = false
	return caps
}

func (oc *OpenClawConnector) GetName() bridgev2.BridgeName {
	return bridgev2.BridgeName{
		DisplayName:          "OpenClaw Bridge",
		NetworkURL:           "https://github.com/openclaw/openclaw",
		NetworkID:            "openclaw",
		BeeperBridgeType:     "openclaw",
		DefaultPort:          29348,
		DefaultCommandPrefix: oc.Config.Bridge.CommandPrefix,
	}
}

func (oc *OpenClawConnector) GetConfig() (example string, data any, upgrader configupgrade.Upgrader) {
	return exampleNetworkConfig, &oc.Config, configupgrade.SimpleUpgrader(upgradeConfig)
}

func (oc *OpenClawConnector) GetDBMetaTypes() database.MetaTypes {
	return database.MetaTypes{
		Portal:    func() any { return &PortalMetadata{} },
		Message:   func() any { return &MessageMetadata{} },
		UserLogin: func() any { return &UserLoginMetadata{} },
		Ghost:     func() any { return &GhostMetadata{} },
	}
}

func (oc *OpenClawConnector) LoadUserLogin(_ context.Context, login *bridgev2.UserLogin) error {
	meta := loginMetadata(login)
	if !strings.EqualFold(strings.TrimSpace(meta.Provider), ProviderOpenClaw) {
		login.Client = &bridgeadapter.BrokenLoginClient{UserLogin: login, Reason: "This bridge only supports OpenClaw logins."}
		return nil
	}
	return bridgeadapter.LoadUserLogin(login, bridgeadapter.LoadUserLoginConfig[*OpenClawClient]{
		Mu: &oc.clientsMu, Clients: oc.clients, BridgeName: "OpenClaw",
		Update: func(e *OpenClawClient, l *bridgev2.UserLogin) { e.UserLogin = l },
		Create: func(l *bridgev2.UserLogin) (*OpenClawClient, error) { return newOpenClawClient(l, oc) },
	})
}

func (oc *OpenClawConnector) GetLoginFlows() []bridgev2.LoginFlow {
	return bridgeadapter.SingleLoginFlow(oc.openClawEnabled(), bridgev2.LoginFlow{
		ID:          ProviderOpenClaw,
		Name:        "OpenClaw",
		Description: "Create a login for an OpenClaw gateway.",
	})
}

func (oc *OpenClawConnector) CreateLogin(_ context.Context, user *bridgev2.User, flowID string) (bridgev2.LoginProcess, error) {
	if err := bridgeadapter.ValidateSingleLoginFlow(flowID, ProviderOpenClaw, oc.openClawEnabled()); err != nil {
		return nil, err
	}
	return &OpenClawLogin{User: user, Connector: oc}, nil
}

func (oc *OpenClawConnector) openClawEnabled() bool {
	return oc.Config.OpenClaw.Enabled == nil || *oc.Config.OpenClaw.Enabled
}
