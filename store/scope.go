package store

import (
	"strings"

	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote/pkg/aidb"
)

// Scope is a typed handle over the shared child DB for one bridge/login/agent
// tuple. Individual stores derive their queries from this scope.
type Scope struct {
	DB       *dbutil.Database
	BridgeID string
	LoginID  string
	AgentID  string
}

func NewScope(db *dbutil.Database, bridgeID, loginID, agentID string) *Scope {
	if db == nil {
		return nil
	}
	return &Scope{
		DB:       db,
		BridgeID: strings.TrimSpace(bridgeID),
		LoginID:  strings.TrimSpace(loginID),
		AgentID:  strings.TrimSpace(agentID),
	}
}

func NewScopeForLogin(login *bridgev2.UserLogin, agentID string) *Scope {
	if login == nil || login.Bridge == nil || login.Bridge.DB == nil {
		return nil
	}
	db := aidb.NewChild(login.Bridge.DB.Database, dbutil.NoopLogger)
	if db == nil {
		return nil
	}
	return NewScope(db, string(login.Bridge.DB.BridgeID), string(login.ID), agentID)
}

func (s *Scope) Sessions() *SessionStore {
	return &SessionStore{scope: s}
}

func (s *Scope) SystemEvents() *SystemEventStore {
	return &SystemEventStore{scope: s}
}

func (s *Scope) Approvals() *ApprovalStore {
	return &ApprovalStore{scope: s}
}
