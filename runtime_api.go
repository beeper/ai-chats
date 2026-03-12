package agentremote

import (
	"strings"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote/store"
)

// RuntimeConfig describes the bridge-scoped inputs required to construct the
// public agentremote runtime facade.
type RuntimeConfig struct {
	Bridge  *bridgev2.Bridge
	Login   *bridgev2.UserLogin
	AgentID string
}

// Runtime is the top-level bridge builder entrypoint. It groups the managed
// turn, approval, and store services for a specific login scope.
type Runtime struct {
	Bridge    *bridgev2.Bridge
	Login     *bridgev2.UserLogin
	AgentID   string
	Turns     *TurnManager
	Approvals *ApprovalManager[map[string]any]
	Stores    *store.Scope
}

// NewRuntime constructs the shared agentremote runtime facade for a single
// bridge/login scope.
func NewRuntime(cfg RuntimeConfig) *Runtime {
	bridge := cfg.Bridge
	if bridge == nil && cfg.Login != nil {
		bridge = cfg.Login.Bridge
	}
	agentID := strings.TrimSpace(cfg.AgentID)
	rt := &Runtime{
		Bridge:  bridge,
		Login:   cfg.Login,
		AgentID: agentID,
		Stores:  store.NewScopeForLogin(cfg.Login, agentID),
	}
	rt.Turns = NewTurnManager(rt)
	rt.Approvals = NewApprovalManager(ApprovalFlowConfig[map[string]any]{
		Login: func() *bridgev2.UserLogin {
			return cfg.Login
		},
	})
	return rt
}
