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

	"github.com/beeper/agentremote/pkg/bridgeadapter"
)

var (
	_ bridgev2.NetworkConnector               = (*OpenCodeConnector)(nil)
	_ bridgev2.PortalBridgeInfoFillingNetwork = (*OpenCodeConnector)(nil)
)

type OpenCodeConnector struct {
	bridgeadapter.BaseConnectorMethods
	br     *bridgev2.Bridge
	Config Config

	clientsMu sync.Mutex
	clients   map[networkid.UserLoginID]bridgev2.NetworkAPI
}

func NewConnector() *OpenCodeConnector {
	return &OpenCodeConnector{
		BaseConnectorMethods: bridgeadapter.BaseConnectorMethods{ProtocolID: "ai-opencode"},
	}
}

func (oc *OpenCodeConnector) Init(bridge *bridgev2.Bridge) {
	oc.br = bridge
	bridgeadapter.EnsureClientMap(&oc.clientsMu, &oc.clients)
}

func (oc *OpenCodeConnector) Start(_ context.Context) error {
	if oc.Config.Bridge.CommandPrefix == "" {
		oc.Config.Bridge.CommandPrefix = "!opencode"
	}
	if oc.Config.OpenCode.Enabled == nil {
		oc.Config.OpenCode.Enabled = ptr.Ptr(true)
	}
	return nil
}

func (oc *OpenCodeConnector) Stop(_ context.Context) {
	bridgeadapter.StopClients(&oc.clientsMu, &oc.clients)
}


func (oc *OpenCodeConnector) GetName() bridgev2.BridgeName {
	return bridgev2.BridgeName{
		DisplayName:          "OpenCode Bridge",
		NetworkURL:           "https://opencode.ai",
		NetworkID:            "opencode",
		BeeperBridgeType:     "opencode",
		DefaultPort:          29347,
		DefaultCommandPrefix: oc.Config.Bridge.CommandPrefix,
	}
}

func (oc *OpenCodeConnector) GetConfig() (example string, data any, upgrader configupgrade.Upgrader) {
	return exampleNetworkConfig, &oc.Config, configupgrade.SimpleUpgrader(upgradeConfig)
}

func (oc *OpenCodeConnector) GetDBMetaTypes() database.MetaTypes {
	return bridgeadapter.BuildMetaTypes(
		func() any { return &PortalMetadata{} },
		func() any { return &MessageMetadata{} },
		func() any { return &UserLoginMetadata{} },
		func() any { return &GhostMetadata{} },
	)
}

func (oc *OpenCodeConnector) LoadUserLogin(_ context.Context, login *bridgev2.UserLogin) error {
	meta := loginMetadata(login)
	if !strings.EqualFold(strings.TrimSpace(meta.Provider), ProviderOpenCode) {
		login.Client = &bridgeadapter.BrokenLoginClient{UserLogin: login, Reason: "This bridge only supports OpenCode logins."}
		return nil
	}

	client, err := bridgeadapter.LoadOrCreateTypedClient(
		&oc.clientsMu,
		oc.clients,
		login,
		func(existing *OpenCodeClient, login *bridgev2.UserLogin) {
			existing.UserLogin = login
		},
		func() (*OpenCodeClient, error) {
			return newOpenCodeClient(login, oc)
		},
	)
	if err != nil {
		login.Client = &bridgeadapter.BrokenLoginClient{UserLogin: login, Reason: "Couldn't initialize OpenCode for this login."}
		return nil
	}
	login.Client = client
	return nil
}

func (oc *OpenCodeConnector) GetLoginFlows() []bridgev2.LoginFlow {
	if !oc.openCodeEnabled() {
		return nil
	}
	return []bridgev2.LoginFlow{
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
}

func (oc *OpenCodeConnector) CreateLogin(_ context.Context, user *bridgev2.User, flowID string) (bridgev2.LoginProcess, error) {
	if !oc.openCodeEnabled() {
		return nil, bridgev2.ErrNotLoggedIn
	}
	if !slices.ContainsFunc(oc.GetLoginFlows(), func(flow bridgev2.LoginFlow) bool {
		return flow.ID == flowID
	}) {
		return nil, bridgev2.ErrInvalidLoginFlowID
	}
	return &OpenCodeLogin{User: user, Connector: oc, FlowID: flowID}, nil
}

func (oc *OpenCodeConnector) openCodeEnabled() bool {
	return oc.Config.OpenCode.Enabled == nil || *oc.Config.OpenCode.Enabled
}
