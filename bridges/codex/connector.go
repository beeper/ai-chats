package codex

import (
	"context"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"go.mau.fi/util/configupgrade"
	"go.mau.fi/util/dbutil"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/ai-bridge/bridges/codex/codexrpc"
	"github.com/beeper/ai-bridge/pkg/aidb"
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

const (
	FlowCodexAPIKey                = "codex_api_key"
	FlowCodexChatGPT               = "codex_chatgpt"
	FlowCodexChatGPTExternalTokens = "codex_chatgpt_external_tokens"
)

func (cc *CodexConnector) Init(bridge *bridgev2.Bridge) {
	cc.br = bridge
	if bridge != nil && bridge.DB != nil && bridge.DB.Database != nil {
		cc.db = aidb.NewChild(
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
	if err := aidb.Upgrade(ctx, db, "codex_bridge", "codex bridge database not initialized"); err != nil {
		return err
	}

	cc.applyRuntimeDefaults()
	cc.primeUserLoginCache(ctx)
	cc.autoProvisionExistingCodex(ctx)

	return nil
}

func (cc *CodexConnector) bridgeDB() *dbutil.Database {
	if cc.db != nil {
		return cc.db
	}
	if cc.br != nil && cc.br.DB != nil {
		cc.db = aidb.NewChild(
			cc.br.DB.Database,
			dbutil.ZeroLogger(cc.br.Log.With().Str("db_section", "codex_bridge").Logger()),
		)
		return cc.db
	}
	return nil
}

func (cc *CodexConnector) primeUserLoginCache(ctx context.Context) {
	bridgeadapter.PrimeUserLoginCache(ctx, cc.br)
}

// autoProvisionExistingCodex checks whether the system `codex` CLI is already
// authenticated and, if so, creates a Codex login for every Matrix user that
// doesn't already have one. This lets users skip the manual login step when
// codex is pre-authenticated (e.g. via `codex auth login`).
func (cc *CodexConnector) autoProvisionExistingCodex(ctx context.Context) {
	if !cc.codexEnabled() {
		return
	}
	cmd := "codex"
	if cc.Config.Codex != nil && strings.TrimSpace(cc.Config.Codex.Command) != "" {
		cmd = strings.TrimSpace(cc.Config.Codex.Command)
	}
	if _, err := exec.LookPath(cmd); err != nil {
		return
	}

	launch, err := cc.resolveAppServerLaunch()
	if err != nil {
		return
	}

	// Spawn a temporary app-server without CODEX_HOME override so it picks up
	// the system's default Codex auth (~/.codex or $CODEX_HOME).
	probeCtx, probeCancel := context.WithTimeout(ctx, 30*time.Second)
	defer probeCancel()
	rpc, err := codexrpc.StartProcess(probeCtx, codexrpc.ProcessConfig{
		Command:      cmd,
		Args:         launch.Args,
		Env:          nil, // inherit system env — use default Codex auth
		WebSocketURL: launch.WebSocketURL,
	})
	if err != nil {
		cc.br.Log.Debug().Err(err).Msg("Auto-provision: failed to start probe codex app-server")
		return
	}
	defer func() { _ = rpc.Close() }()

	ci := cc.Config.Codex.ClientInfo
	initCtx, initCancel := context.WithTimeout(probeCtx, 20*time.Second)
	_, err = rpc.Initialize(initCtx, codexrpc.ClientInfo{Name: ci.Name, Title: ci.Title, Version: ci.Version}, false)
	initCancel()
	if err != nil {
		cc.br.Log.Debug().Err(err).Msg("Auto-provision: codex initialize failed")
		return
	}

	var resp struct {
		Account *codexAccountInfo `json:"account"`
	}
	readCtx, readCancel := context.WithTimeout(probeCtx, 10*time.Second)
	err = rpc.Call(readCtx, "account/read", map[string]any{"refreshToken": false}, &resp)
	readCancel()
	if err != nil || resp.Account == nil {
		cc.br.Log.Debug().Err(err).Msg("Auto-provision: system codex is not authenticated")
		return
	}

	cc.br.Log.Debug().
		Str("account_type", resp.Account.Type).
		Str("account_email", resp.Account.Email).
		Msg("Auto-provision: detected existing Codex authentication")

	userIDs, err := cc.br.DB.UserLogin.GetAllUserIDsWithLogins(ctx)
	if err != nil {
		cc.br.Log.Warn().Err(err).Msg("Auto-provision: failed to list user IDs")
		return
	}

	for _, mxid := range userIDs {
		user, err := cc.br.GetUserByMXID(ctx, mxid)
		if err != nil || user == nil {
			continue
		}

		// Check if this user already has a Codex login.
		hasCodex := false
		for _, existing := range user.GetUserLogins() {
			if existing == nil || existing.Metadata == nil {
				continue
			}
			meta, ok := existing.Metadata.(*UserLoginMetadata)
			if !ok || meta == nil {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(meta.Provider), ProviderCodex) {
				hasCodex = true
				break
			}
		}
		if hasCodex {
			continue
		}

		// Use a deterministic instance ID so restarts won't create duplicates.
		loginID := bridgeadapter.MakeUserLoginID("codex", mxid, 1)

		// If this login already exists in the DB (e.g. from a previous run), skip creation.
		existing, err := cc.br.GetExistingUserLoginByID(ctx, loginID)
		if err != nil {
			cc.br.Log.Debug().Err(err).Stringer("mxid", mxid).Msg("Auto-provision: failed to check existing login")
			continue
		}
		if existing != nil {
			continue
		}

		meta := &UserLoginMetadata{
			Provider:          ProviderCodex,
			CodexHome:         "",    // empty = use system default
			CodexHomeManaged:  false, // don't delete the user's own Codex home on logout
			CodexAuthMode:     resp.Account.Type,
			CodexAccountEmail: resp.Account.Email,
		}

		login, err := user.NewLogin(ctx, &database.UserLogin{
			ID:         loginID,
			RemoteName: "Codex",
			Metadata:   meta,
		}, nil)
		if err != nil {
			cc.br.Log.Warn().Err(err).Stringer("mxid", mxid).Msg("Auto-provision: failed to create login")
			continue
		}

		if err := cc.LoadUserLogin(ctx, login); err != nil {
			cc.br.Log.Warn().Err(err).Stringer("mxid", mxid).Msg("Auto-provision: failed to load login")
			continue
		}

		cc.br.Log.Info().
			Stringer("mxid", mxid).
			Str("login_id", string(login.ID)).
			Msg("Auto-provisioned Codex login for user")
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
	return bridgeadapter.DefaultNetworkCapabilities()
}

func (cc *CodexConnector) GetBridgeInfoVersion() (info, capabilities int) {
	return bridgeadapter.DefaultBridgeInfoVersion()
}

func (cc *CodexConnector) FillPortalBridgeInfo(portal *bridgev2.Portal, content *event.BridgeEventContent) {
	content.BeeperRoomTypeV2 = "codex"
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
	return bridgeadapter.BuildMetaTypes(
		func() any { return &PortalMetadata{} },
		func() any { return &MessageMetadata{} },
		func() any { return &UserLoginMetadata{} },
		func() any { return &GhostMetadata{} },
	)
}

func (cc *CodexConnector) LoadUserLogin(_ context.Context, login *bridgev2.UserLogin) error {
	meta := loginMetadata(login)
	if !strings.EqualFold(strings.TrimSpace(meta.Provider), ProviderCodex) {
		login.Client = newBrokenLoginClient(login, cc, "This bridge only supports Codex logins.")
		return nil
	}
	return cc.loadCodexUserLogin(login)
}

func (cc *CodexConnector) loadCodexUserLogin(login *bridgev2.UserLogin) error {
	if !cc.codexEnabled() {
		login.Client = newBrokenLoginClient(login, cc, "Codex integration is disabled in the configuration.")
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
		login.Client = newBrokenLoginClient(login, cc, "Couldn't initialize Codex for this login. Remove and re-add the account.")
		return nil
	}
	login.Client = client
	if codexClient, ok := client.(*CodexClient); ok {
		codexClient.scheduleBootstrap()
	}
	return nil
}

func (cc *CodexConnector) GetLoginFlows() []bridgev2.LoginFlow {
	if !cc.codexEnabled() {
		return nil
	}
	return []bridgev2.LoginFlow{
		{
			ID:          FlowCodexAPIKey,
			Name:        "API Key",
			Description: "Sign in with an OpenAI API key using codex app-server.",
		},
		{
			ID:          FlowCodexChatGPT,
			Name:        "ChatGPT",
			Description: "Open browser login and authenticate with your ChatGPT account.",
		},
		{
			ID:          FlowCodexChatGPTExternalTokens,
			Name:        "ChatGPT external tokens",
			Description: "Provide externally managed ChatGPT id/access tokens.",
		},
	}
}

func (cc *CodexConnector) CreateLogin(_ context.Context, user *bridgev2.User, flowID string) (bridgev2.LoginProcess, error) {
	if !cc.codexEnabled() {
		return nil, fmt.Errorf("login flow %s is not available", flowID)
	}
	if !slices.ContainsFunc(cc.GetLoginFlows(), func(f bridgev2.LoginFlow) bool { return f.ID == flowID }) {
		return nil, fmt.Errorf("login flow %s is not available", flowID)
	}
	return &CodexLogin{User: user, Connector: cc, FlowID: flowID}, nil
}

func (cc *CodexConnector) codexEnabled() bool {
	return cc.Config.Codex == nil || cc.Config.Codex.Enabled == nil || *cc.Config.Codex.Enabled
}
