package codex

import (
	"context"
	"os/exec"
	"strings"
	"sync"
	"time"

	"go.mau.fi/util/dbutil"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote"
	"github.com/beeper/agentremote/bridges/codex/codexrpc"
	bridgesdk "github.com/beeper/agentremote/sdk"
)

var (
	_ bridgev2.NetworkConnector               = (*CodexConnector)(nil)
	_ bridgev2.PortalBridgeInfoFillingNetwork = (*CodexConnector)(nil)
)

// CodexConnector runs the dedicated Codex bridge surface.
type CodexConnector struct {
	*agentremote.ConnectorBase
	br        *bridgev2.Bridge
	Config    Config
	sdkConfig *bridgesdk.Config
	db        *dbutil.Database

	clientsMu sync.Mutex
	clients   map[networkid.UserLoginID]bridgev2.NetworkAPI
}

const (
	FlowCodexAPIKey                = "codex_api_key"
	FlowCodexChatGPT               = "codex_chatgpt"
	FlowCodexChatGPTExternalTokens = "codex_chatgpt_external_tokens"
	hostAuthLoginPrefix            = "codex_host"
	hostAuthRemoteName             = "Codex (host auth)"
)

type hostAuthProbe struct {
	AuthMode     string
	AccountEmail string
}

func (cc *CodexConnector) bridgeDB() *dbutil.Database {
	return cc.db
}

// reconcileHostAuthLogins ensures a deterministic host-auth Codex login exists
// for all known Matrix users when the local/default Codex auth is already valid.
func (cc *CodexConnector) reconcileHostAuthLogins(ctx context.Context) {
	if !cc.codexEnabled() || cc.br == nil || cc.br.DB == nil {
		return
	}

	probe, err := cc.probeHostAuth(ctx)
	if err != nil {
		cc.br.Log.Debug().Err(err).Msg("Host-auth reconcile: failed to probe Codex auth")
		return
	}
	if probe == nil {
		return
	}

	userIDs, err := cc.getKnownUserIDs(ctx)
	if err != nil {
		cc.br.Log.Warn().Err(err).Msg("Host-auth reconcile: failed to list known users")
		return
	}
	for _, mxid := range userIDs {
		user, err := cc.br.GetUserByMXID(ctx, mxid)
		if err != nil || user == nil {
			continue
		}
		if err := cc.ensureHostAuthLoginForUserWithProbe(ctx, user, probe); err != nil {
			cc.br.Log.Warn().
				Err(err).
				Stringer("mxid", mxid).
				Msg("Host-auth reconcile: failed to ensure host-auth login")
		}
	}
}

func (cc *CodexConnector) getKnownUserIDs(ctx context.Context) ([]id.UserID, error) {
	if cc == nil || cc.br == nil || cc.br.DB == nil {
		return nil, nil
	}
	rows, err := cc.br.DB.Query(ctx, `SELECT mxid FROM "user" WHERE bridge_id=$1`, cc.br.ID)
	return dbutil.NewRowIterWithError(rows, dbutil.ScanSingleColumn[id.UserID], err).AsList()
}

func (cc *CodexConnector) probeHostAuth(ctx context.Context) (*hostAuthProbe, error) {
	if cc == nil || !cc.codexEnabled() {
		return nil, nil
	}
	cmd := cc.resolveCodexCommand()
	if _, err := exec.LookPath(cmd); err != nil {
		return nil, nil
	}

	launch, err := cc.resolveAppServerLaunch()
	if err != nil {
		return nil, err
	}

	probeCtx, probeCancel := context.WithTimeout(ctx, 30*time.Second)
	defer probeCancel()
	rpc, err := codexrpc.StartProcess(probeCtx, codexrpc.ProcessConfig{
		Command:      cmd,
		Args:         launch.Args,
		Env:          nil, // inherit system env and use host/default Codex auth state
		WebSocketURL: launch.WebSocketURL,
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = rpc.Close() }()

	ci := cc.Config.Codex.ClientInfo
	initCtx, initCancel := context.WithTimeout(probeCtx, 20*time.Second)
	_, err = rpc.Initialize(initCtx, codexrpc.ClientInfo{Name: ci.Name, Title: ci.Title, Version: ci.Version}, false)
	initCancel()
	if err != nil {
		return nil, err
	}

	var resp struct {
		Account            *codexAccountInfo `json:"account"`
		RequiresOpenaiAuth bool              `json:"requiresOpenaiAuth"`
	}
	readCtx, readCancel := context.WithTimeout(probeCtx, 10*time.Second)
	err = rpc.Call(readCtx, "account/read", map[string]any{"refreshToken": false}, &resp)
	readCancel()
	if err != nil {
		return nil, err
	}
	if resp.Account == nil {
		return nil, nil
	}

	probe := &hostAuthProbe{
		AuthMode:     strings.TrimSpace(resp.Account.Type),
		AccountEmail: strings.TrimSpace(resp.Account.Email),
	}
	return probe, nil
}

func (cc *CodexConnector) ensureHostAuthLoginForUser(ctx context.Context, user *bridgev2.User) error {
	probe, err := cc.probeHostAuth(ctx)
	if err != nil || probe == nil {
		return err
	}
	return cc.ensureHostAuthLoginForUserWithProbe(ctx, user, probe)
}

func (cc *CodexConnector) ensureHostAuthLoginForUserWithProbe(ctx context.Context, user *bridgev2.User, probe *hostAuthProbe) error {
	if cc == nil || cc.br == nil || user == nil || probe == nil {
		return nil
	}
	loginID := cc.hostAuthLoginID(user.MXID)
	if hasManagedCodexLogin(user.GetUserLogins(), loginID) {
		cc.br.Log.Debug().
			Stringer("mxid", user.MXID).
			Msg("Host-auth reconcile: skipping host-auth login because a managed Codex login exists")
		return nil
	}
	existing, err := cc.br.GetExistingUserLoginByID(ctx, loginID)
	if err != nil {
		return err
	}
	meta := &UserLoginMetadata{
		Provider:          ProviderCodex,
		CodexAuthSource:   CodexAuthSourceHost,
		CodexAuthMode:     strings.TrimSpace(probe.AuthMode),
		CodexAccountEmail: strings.TrimSpace(probe.AccountEmail),
	}
	login, err := user.NewLogin(ctx, &database.UserLogin{
		ID:         loginID,
		RemoteName: hostAuthRemoteName,
		Metadata:   meta,
	}, nil)
	if err != nil {
		return err
	}
	if client, ok := login.Client.(*CodexClient); ok && client != nil && !client.IsLoggedIn() {
		bg := context.Background()
		if cc.br.BackgroundCtx != nil {
			bg = cc.br.BackgroundCtx
		}
		go login.Client.Connect(login.Log.WithContext(bg))
	}
	logger := cc.br.Log.With().
		Stringer("mxid", user.MXID).
		Str("login_id", string(login.ID)).
		Logger()
	if existing == nil {
		logger.Info().Msg("Host-auth reconcile: created host-auth Codex login")
	} else {
		logger.Debug().Msg("Host-auth reconcile: updated host-auth Codex login metadata")
	}
	return nil
}

func (cc *CodexConnector) hostAuthLoginID(mxid id.UserID) networkid.UserLoginID {
	return agentremote.MakeUserLoginID(hostAuthLoginPrefix, mxid, 1)
}

func hasManagedCodexLogin(logins []*bridgev2.UserLogin, exceptID networkid.UserLoginID) bool {
	for _, existing := range logins {
		if existing == nil || existing.ID == exceptID || existing.Metadata == nil {
			continue
		}
		meta, ok := existing.Metadata.(*UserLoginMetadata)
		if !ok || meta == nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(meta.Provider), ProviderCodex) && isManagedAuthLogin(meta) {
			return true
		}
	}
	return false
}

func resolveCodexCommandFromConfig(cfg *CodexConfig) string {
	if cfg == nil {
		return "codex"
	}
	if cmd := strings.TrimSpace(cfg.Command); cmd != "" {
		return cmd
	}
	return "codex"
}

func (cc *CodexConnector) resolveCodexCommand() string {
	if cc == nil {
		return "codex"
	}
	return resolveCodexCommandFromConfig(cc.Config.Codex)
}

func (cc *CodexConnector) applyRuntimeDefaults() {
	if cc.Config.ModelCacheDuration == 0 {
		cc.Config.ModelCacheDuration = 6 * time.Hour
	}
	bridgesdk.ApplyDefaultCommandPrefix(&cc.Config.Bridge.CommandPrefix, "!ai")
	if cc.Config.Codex == nil {
		cc.Config.Codex = &CodexConfig{}
	}
	bridgesdk.ApplyBoolDefault(&cc.Config.Codex.Enabled, true)
	if strings.TrimSpace(cc.Config.Codex.Command) == "" {
		cc.Config.Codex.Command = "codex"
	}
	if strings.TrimSpace(cc.Config.Codex.DefaultModel) == "" {
		cc.Config.Codex.DefaultModel = "gpt-5.1-codex"
	}
	bridgesdk.ApplyBoolDefault(&cc.Config.Codex.NetworkAccess, true)
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

func (cc *CodexConnector) codexEnabled() bool {
	if cc.Config.Codex == nil || cc.Config.Codex.Enabled == nil {
		return true
	}
	return *cc.Config.Codex.Enabled
}
