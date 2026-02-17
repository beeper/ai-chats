package codex

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.mau.fi/util/configupgrade"
	"go.mau.fi/util/dbutil"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/ai-bridge/pkg/bridgeadapter"
)

var (
	_ bridgev2.NetworkConnector               = (*CodexConnector)(nil)
	_ bridgev2.PortalBridgeInfoFillingNetwork = (*CodexConnector)(nil)
)

// CodexConnector runs the dedicated Codex bridge surface.
type CodexConnector struct {
	br     *bridgev2.Bridge
	Config Config
	db     *dbutil.Database

	clientsMu sync.Mutex
	clients   map[networkid.UserLoginID]bridgev2.NetworkAPI
}

func (cc *CodexConnector) Init(bridge *bridgev2.Bridge) {
	cc.br = bridge
	cc.db = nil
	if bridge != nil && bridge.DB != nil && bridge.DB.Database != nil {
		cc.db = makeCodexBridgeChildDB(
			bridge.DB.Database,
			dbutil.ZeroLogger(bridge.Log.With().Str("db_section", "codex_bridge").Logger()),
		)
	}
	bridgeadapter.EnsureClientMap(&cc.clientsMu, &cc.clients)
}

func (cc *CodexConnector) Stop(ctx context.Context) {
	bridgeadapter.StopClients(&cc.clientsMu, &cc.clients)
}

func (cc *CodexConnector) Start(ctx context.Context) error {
	db := cc.bridgeDB()
	if err := bridgeadapter.UpgradeChildDB(ctx, db, "codex_bridge", "codex bridge database not initialized"); err != nil {
		return err
	}

	cc.applyRuntimeDefaults()
	cc.primeUserLoginCache(ctx)

	return nil
}

func (cc *CodexConnector) primeUserLoginCache(ctx context.Context) {
	if cc == nil {
		return
	}
	bridgeadapter.PrimeUserLoginCache(ctx, cc.br)
}

func (cc *CodexConnector) applyRuntimeDefaults() {
	if cc.Config.ModelCacheDuration == 0 {
		cc.Config.ModelCacheDuration = 6 * time.Hour
	}
	if cc.Config.Bridge.CommandPrefix == "" {
		cc.Config.Bridge.CommandPrefix = "!ai"
	}
	if cc.Config.Codex == nil {
		cc.Config.Codex = &CodexConfig{}
	}
	if cc.Config.Codex.Enabled == nil {
		v := true
		cc.Config.Codex.Enabled = &v
	}
	if strings.TrimSpace(cc.Config.Codex.Command) == "" {
		cc.Config.Codex.Command = "codex"
	}
	if strings.TrimSpace(cc.Config.Codex.DefaultModel) == "" {
		cc.Config.Codex.DefaultModel = "gpt-5.1-codex"
	}
	if cc.Config.Codex.NetworkAccess == nil {
		v := true
		cc.Config.Codex.NetworkAccess = &v
	}
	if cc.Config.Codex.ClientInfo == nil {
		cc.Config.Codex.ClientInfo = &CodexClientInfo{}
	}
	if strings.TrimSpace(cc.Config.Codex.ClientInfo.Name) == "" {
		cc.Config.Codex.ClientInfo.Name = "ai_bridge_matrix"
	}
	if strings.TrimSpace(cc.Config.Codex.ClientInfo.Title) == "" {
		cc.Config.Codex.ClientInfo.Title = "AI Bridge (Matrix)"
	}
	if strings.TrimSpace(cc.Config.Codex.ClientInfo.Version) == "" {
		cc.Config.Codex.ClientInfo.Version = "0.1.0"
	}
}

func (cc *CodexConnector) GetCapabilities() *bridgev2.NetworkGeneralCapabilities {
	return bridgeadapter.DefaultNetworkCapabilities()
}

func (cc *CodexConnector) GetBridgeInfoVersion() (info, capabilities int) {
	return bridgeadapter.DefaultBridgeInfoVersion()
}

func (cc *CodexConnector) FillPortalBridgeInfo(portal *bridgev2.Portal, content *event.BridgeEventContent) {
	meta := portalMeta(portal)
	if meta.IsCronRoom {
		content.BeeperRoomTypeV2 = "cron"
	} else {
		content.BeeperRoomTypeV2 = "codex"
	}
}

func (cc *CodexConnector) GetName() bridgev2.BridgeName {
	return bridgev2.BridgeName{
		DisplayName:          "Codex Bridge",
		NetworkURL:           "https://github.com/openai/codex",
		NetworkID:            "codex",
		BeeperBridgeType:     "codex",
		DefaultPort:          29346,
		DefaultCommandPrefix: cc.Config.Bridge.CommandPrefix,
	}
}

func (cc *CodexConnector) GetConfig() (example string, data any, upgrader configupgrade.Upgrader) {
	return exampleNetworkConfig, &cc.Config, configupgrade.SimpleUpgrader(upgradeConfig)
}

func (cc *CodexConnector) GetDBMetaTypes() database.MetaTypes {
	return bridgeadapter.MetaTypes(
		func() any { return &PortalMetadata{} },
		func() any { return &MessageMetadata{} },
		func() any { return &UserLoginMetadata{} },
		func() any { return &GhostMetadata{} },
	)
}

func (cc *CodexConnector) LoadUserLogin(ctx context.Context, login *bridgev2.UserLogin) error {
	_ = ctx
	meta := loginMetadata(login)
	if !strings.EqualFold(strings.TrimSpace(meta.Provider), ProviderCodex) {
		login.Client = newBrokenLoginClient(login, "This bridge only supports Codex logins.")
		return nil
	}
	return cc.loadCodexUserLogin(login)
}

func (cc *CodexConnector) loadCodexUserLogin(login *bridgev2.UserLogin) error {
	if !cc.codexEnabled() {
		login.Client = newBrokenLoginClient(login, "Codex integration is disabled in the configuration.")
		return nil
	}

	client, err := bridgeadapter.LoadOrCreateClient(
		&cc.clientsMu,
		cc.clients,
		login.ID,
		func(existingAPI bridgev2.NetworkAPI) bool {
			existing, ok := existingAPI.(*CodexClient)
			if !ok || existing == nil {
				return false
			}
			existing.UserLogin = login
			login.Client = existing
			return true
		},
		func() (bridgev2.NetworkAPI, error) {
			return newCodexClient(login, cc)
		},
	)
	if err != nil {
		login.Client = newBrokenLoginClient(login, "Couldn't initialize Codex for this login. Remove and re-add the account.")
		return nil
	}
	login.Client = client
	return nil
}

func (cc *CodexConnector) GetLoginFlows() []bridgev2.LoginFlow {
	flows := []bridgev2.LoginFlow{}
	if cc.codexEnabled() {
		flows = append(flows, bridgev2.LoginFlow{
			ID:          ProviderCodex,
			Name:        "Codex",
			Description: "Use a local Codex install via codex app-server (stdio).",
		})
	}
	return flows
}

func (cc *CodexConnector) CreateLogin(ctx context.Context, user *bridgev2.User, flowID string) (bridgev2.LoginProcess, error) {
	if flowID != ProviderCodex {
		return nil, fmt.Errorf("login flow %s is not available", flowID)
	}
	if !cc.codexEnabled() {
		return nil, fmt.Errorf("login flow %s is not available", flowID)
	}
	return &CodexLogin{User: user, Connector: cc, FlowID: flowID}, nil
}

func (cc *CodexConnector) codexEnabled() bool {
	return cc.Config.Codex == nil || cc.Config.Codex.Enabled == nil || *cc.Config.Codex.Enabled
}
