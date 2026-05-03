package codex

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/beeper/agentremote/bridges/codex/codexrpc"
	"github.com/beeper/agentremote/sdk"
)

func (cl *CodexLogin) logger(ctx context.Context) *zerolog.Logger {
	var l zerolog.Logger
	if cl != nil && cl.User != nil {
		l = cl.User.Log.With().Str("component", "codex_login").Logger()
	} else {
		l = zerolog.Nop()
	}
	return sdk.LoggerFromContext(ctx, &l)
}
func (cl *CodexLogin) Cancel() {
	cl.cancelLoginAttempt(true)
}

func (cl *CodexLogin) getRPC() *codexrpc.Client {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	return cl.rpc
}

func (cl *CodexLogin) setRPC(rpc *codexrpc.Client) {
	cl.mu.Lock()
	cl.rpc = rpc
	cl.mu.Unlock()
}

func (cl *CodexLogin) getLoginID() string {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	return cl.loginID
}

func (cl *CodexLogin) getAuthURL() string {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	return cl.authURL
}

func (cl *CodexLogin) getAuthMode() string {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	return cl.authMode
}

func (cl *CodexLogin) setAuthMode(mode string) {
	cl.mu.Lock()
	cl.authMode = mode
	cl.mu.Unlock()
}

func (cl *CodexLogin) setLoginSession(loginID, authURL string) {
	cl.mu.Lock()
	cl.loginID = loginID
	cl.authURL = authURL
	cl.mu.Unlock()
}

// signalStart sends a non-blocking signal on startCh.
func (cl *CodexLogin) signalStart(err error) {
	select {
	case cl.startCh <- err:
	default:
	}
}
func (cl *CodexLogin) backgroundProcessContext() context.Context {
	if cl.Connector != nil && cl.Connector.br != nil && cl.Connector.br.BackgroundCtx != nil {
		return cl.Connector.br.BackgroundCtx
	}
	return context.Background()
}

func (cl *CodexLogin) initializeExperimental(_ string) bool {
	// The bridge relies on persistExtendedHistory for thread recovery, which is
	// gated behind the experimental API capability in current Codex app-server builds.
	return true
}

func (cl *CodexLogin) cancelLoginAttempt(removeHome bool) {
	cl.mu.Lock()
	rpc := cl.rpc
	cl.rpc = nil
	cancel := cl.cancel
	cl.cancel = nil
	loginID := cl.loginID
	authMode := cl.authMode
	codexHome := cl.codexHome
	if removeHome {
		cl.codexHome = ""
		cl.chatgptAccountID = ""
		cl.chatgptPlanType = ""
	}
	cl.mu.Unlock()

	if rpc != nil && strings.TrimSpace(loginID) != "" && strings.TrimSpace(authMode) == "chatgpt" {
		callCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		var out struct{}
		_ = rpc.Call(callCtx, "account/login/cancel", map[string]any{"loginId": loginID}, &out)
		stop()
	}
	if cancel != nil {
		cancel()
	}
	if rpc != nil {
		_ = rpc.Close()
	}
	if removeHome && strings.TrimSpace(codexHome) != "" {
		_ = os.RemoveAll(codexHome)
	}
}

// spawnAndStartLogin creates an isolated CODEX_HOME, spawns an app-server, and starts auth.
