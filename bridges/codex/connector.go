package codex

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"sync"
	"time"

	"go.mau.fi/util/configupgrade"
	"go.mau.fi/util/dbutil"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
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
	cc.clientsMu.Lock()
	if cc.clients == nil {
		cc.clients = make(map[networkid.UserLoginID]bridgev2.NetworkAPI)
	}
	cc.clientsMu.Unlock()
}

func (cc *CodexConnector) Stop(ctx context.Context) {
	cc.clientsMu.Lock()
	clients := maps.Clone(cc.clients)
	cc.clientsMu.Unlock()

	for _, client := range clients {
		if dc, ok := client.(interface{ Disconnect() }); ok {
			dc.Disconnect()
		}
	}
}

func (cc *CodexConnector) Start(ctx context.Context) error {
	db := cc.bridgeDB()
	if db == nil {
		return bridgev2.DBUpgradeError{Err: errors.New("codex bridge database not initialized"), Section: "codex_bridge"}
	}
	if err := db.Upgrade(ctx); err != nil {
		return bridgev2.DBUpgradeError{Err: err, Section: "codex_bridge"}
	}

	cc.applyRuntimeDefaults()
	cc.primeUserLoginCache(ctx)

	return nil
}

func (cc *CodexConnector) primeUserLoginCache(ctx context.Context) {
	if cc == nil || cc.br == nil || cc.br.DB == nil || cc.br.DB.UserLogin == nil {
		return
	}
	userIDs, err := cc.br.DB.UserLogin.GetAllUserIDsWithLogins(ctx)
	if err != nil {
		cc.br.Log.Warn().Err(err).Msg("Failed to list users with logins for cache priming")
		return
	}
	for _, mxid := range userIDs {
		_, _ = cc.br.GetUserByMXID(ctx, mxid)
	}
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
	return &bridgev2.NetworkGeneralCapabilities{
		DisappearingMessages: true,
		Provisioning: bridgev2.ProvisioningCapabilities{
			ResolveIdentifier: bridgev2.ResolveIdentifierCapabilities{
				CreateDM:       true,
				LookupUsername: true,
				ContactList:    true,
				Search:         true,
			},
		},
	}
}

func (cc *CodexConnector) GetBridgeInfoVersion() (info, capabilities int) {
	return 1, 3
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
	return database.MetaTypes{
		Portal: func() any {
			return &PortalMetadata{}
		},
		Message: func() any {
			return &MessageMetadata{}
		},
		UserLogin: func() any {
			return &UserLoginMetadata{}
		},
		Ghost: func() any {
			return &GhostMetadata{}
		},
	}
}

func (cc *CodexConnector) LoadUserLogin(ctx context.Context, login *bridgev2.UserLogin) error {
	_ = ctx
	meta := loginMetadata(login)
	if !strings.EqualFold(strings.TrimSpace(meta.Provider), ProviderCodex) {
		login.Client = &brokenLoginClient{UserLogin: login, Reason: "This bridge only supports Codex logins."}
		return nil
	}
	return cc.loadCodexUserLogin(login)
}

func (cc *CodexConnector) loadCodexUserLogin(login *bridgev2.UserLogin) error {
	if cc.Config.Codex != nil && cc.Config.Codex.Enabled != nil && !*cc.Config.Codex.Enabled {
		login.Client = &brokenLoginClient{UserLogin: login, Reason: "Codex integration is disabled in the configuration."}
		return nil
	}

	cc.clientsMu.Lock()
	if existingAPI := cc.clients[login.ID]; existingAPI != nil {
		if existing, ok := existingAPI.(*CodexClient); ok {
			existing.UserLogin = login
			login.Client = existing
			cc.clientsMu.Unlock()
			return nil
		}
		delete(cc.clients, login.ID)
	}
	cc.clientsMu.Unlock()

	client, err := newCodexClient(login, cc)
	if err != nil {
		login.Client = &brokenLoginClient{UserLogin: login, Reason: "Couldn't initialize Codex for this login. Remove and re-add the account."}
		return nil
	}
	cc.clientsMu.Lock()
	cc.clients[login.ID] = client
	cc.clientsMu.Unlock()
	login.Client = client
	return nil
}

func (cc *CodexConnector) GetLoginFlows() []bridgev2.LoginFlow {
	flows := []bridgev2.LoginFlow{}
	if cc.Config.Codex == nil || cc.Config.Codex.Enabled == nil || *cc.Config.Codex.Enabled {
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
	if cc.Config.Codex != nil && cc.Config.Codex.Enabled != nil && !*cc.Config.Codex.Enabled {
		return nil, fmt.Errorf("login flow %s is not available", flowID)
	}
	return &CodexLogin{User: user, Connector: cc, FlowID: flowID}, nil
}
