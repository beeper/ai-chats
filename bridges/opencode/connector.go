package opencode

import (
	"context"
	"strings"
	"sync"

	"go.mau.fi/util/configupgrade"
	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/ai-bridge/pkg/bridgeadapter"
)

var (
	_ bridgev2.NetworkConnector               = (*OpenCodeConnector)(nil)
	_ bridgev2.PortalBridgeInfoFillingNetwork = (*OpenCodeConnector)(nil)
)

type OpenCodeConnector struct {
	br     *bridgev2.Bridge
	Config Config

	clientsMu sync.Mutex
	clients   map[networkid.UserLoginID]bridgev2.NetworkAPI
}

func NewConnector() *OpenCodeConnector {
	return &OpenCodeConnector{}
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

func (oc *OpenCodeConnector) GetCapabilities() *bridgev2.NetworkGeneralCapabilities {
	return bridgeadapter.DefaultNetworkCapabilities()
}

func (oc *OpenCodeConnector) GetBridgeInfoVersion() (info, capabilities int) {
	return bridgeadapter.DefaultBridgeInfoVersion()
}

func (oc *OpenCodeConnector) FillPortalBridgeInfo(portal *bridgev2.Portal, content *event.BridgeEventContent) {
	meta := portalMeta(portal)
	if meta.IsOpenCodeRoom {
		content.BeeperRoomTypeV2 = "opencode"
	} else {
		content.BeeperRoomTypeV2 = "chat"
	}
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
	return database.MetaTypes{
		Portal:    func() any { return &PortalMetadata{} },
		Message:   func() any { return &MessageMetadata{} },
		UserLogin: func() any { return &UserLoginMetadata{} },
		Ghost:     func() any { return &GhostMetadata{} },
	}
}

func (oc *OpenCodeConnector) LoadUserLogin(_ context.Context, login *bridgev2.UserLogin) error {
	meta := loginMetadata(login)
	if !strings.EqualFold(strings.TrimSpace(meta.Provider), ProviderOpenCode) {
		login.Client = &bridgeadapter.BrokenLoginClient{UserLogin: login, Reason: "This bridge only supports OpenCode logins."}
		return nil
	}

	client, err := bridgeadapter.LoadOrCreateClient(
		&oc.clientsMu,
		oc.clients,
		login.ID,
		func(existingAPI bridgev2.NetworkAPI) bool {
			existing, ok := existingAPI.(*OpenCodeClient)
			if !ok || existing == nil {
				return false
			}
			existing.UserLogin = login
			login.Client = existing
			return true
		},
		func() (bridgev2.NetworkAPI, error) {
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
	return bridgeadapter.SingleLoginFlow(oc.openCodeEnabled(), bridgev2.LoginFlow{
		ID:          ProviderOpenCode,
		Name:        "OpenCode",
		Description: "Create a login for an OpenCode server instance.",
	})
}

func (oc *OpenCodeConnector) CreateLogin(_ context.Context, user *bridgev2.User, flowID string) (bridgev2.LoginProcess, error) {
	if err := bridgeadapter.ValidateSingleLoginFlow(flowID, ProviderOpenCode, oc.openCodeEnabled()); err != nil {
		return nil, err
	}
	return &OpenCodeLogin{User: user, Connector: oc}, nil
}

func (oc *OpenCodeConnector) openCodeEnabled() bool {
	return oc.Config.OpenCode.Enabled == nil || *oc.Config.OpenCode.Enabled
}
