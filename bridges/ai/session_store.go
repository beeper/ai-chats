package ai

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"time"
)

type sessionEntry struct {
	UpdatedAt int64
}

type sessionStoreRef struct {
	BridgeID string
	LoginID  string
	AgentID  string
}

var sessionStoreLocks sync.Map

func sessionStoreLockKey(ref sessionStoreRef, sessionKey string) string {
	bridgeID := strings.TrimSpace(ref.BridgeID)
	loginID := strings.TrimSpace(ref.LoginID)
	agent := normalizeAgentID(ref.AgentID)
	key := strings.TrimSpace(sessionKey)
	if key == "" {
		key = "main"
	}
	return bridgeID + "|" + loginID + "|" + agent + "|" + key
}

func sessionStoreLock(ref sessionStoreRef, sessionKey string) *sync.Mutex {
	key := sessionStoreLockKey(ref, sessionKey)
	if val, ok := sessionStoreLocks.Load(key); ok {
		return val.(*sync.Mutex)
	}
	mu := &sync.Mutex{}
	actual, _ := sessionStoreLocks.LoadOrStore(key, mu)
	return actual.(*sync.Mutex)
}

func (oc *AIClient) sessionDBScope() *loginScope {
	return loginScopeForClient(oc)
}

func (oc *AIClient) getSessionEntry(ctx context.Context, ref sessionStoreRef, sessionKey string) (sessionEntry, bool) {
	if oc == nil || strings.TrimSpace(sessionKey) == "" {
		return sessionEntry{}, false
	}
	scope := oc.sessionDBScope()
	if scope == nil {
		return sessionEntry{}, false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var entry sessionEntry
	err := scope.db.QueryRow(ctx, `
		SELECT
			updated_at_ms
		FROM `+aiSessionsTable+`
		WHERE bridge_id=$1 AND login_id=$2 AND store_agent_id=$3 AND session_key=$4
	`,
		scope.bridgeID, scope.loginID, normalizeAgentID(ref.AgentID), strings.TrimSpace(sessionKey),
	).Scan(
		&entry.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return sessionEntry{}, false
	}
	if err != nil {
		oc.Log().Warn().Err(err).Str("session_key", sessionKey).Msg("session store: lookup failed")
		return sessionEntry{}, false
	}
	return entry, true
}

func (oc *AIClient) upsertSessionEntry(ctx context.Context, ref sessionStoreRef, sessionKey string, entry sessionEntry) error {
	scope := oc.sessionDBScope()
	if scope == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	_, err := scope.db.Exec(ctx, `
		INSERT INTO `+aiSessionsTable+` (
			bridge_id,
			login_id,
			store_agent_id,
			session_key,
			updated_at_ms
		) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (bridge_id, login_id, store_agent_id, session_key) DO UPDATE SET
			updated_at_ms=excluded.updated_at_ms
	`,
		scope.bridgeID,
		scope.loginID,
		normalizeAgentID(ref.AgentID),
		strings.TrimSpace(sessionKey),
		entry.UpdatedAt,
	)
	return err
}

func (oc *AIClient) updateSessionTimestamp(ctx context.Context, ref sessionStoreRef, sessionKey string, minUpdatedAt int64) {
	if oc == nil || strings.TrimSpace(sessionKey) == "" {
		return
	}
	lock := sessionStoreLock(ref, sessionKey)
	lock.Lock()
	defer lock.Unlock()

	entry, _ := oc.getSessionEntry(ctx, ref, sessionKey)
	updatedAt := time.Now().UnixMilli()
	if entry.UpdatedAt > updatedAt {
		updatedAt = entry.UpdatedAt
	}
	if minUpdatedAt > updatedAt {
		updatedAt = minUpdatedAt
	}
	entry.UpdatedAt = updatedAt
	if err := oc.upsertSessionEntry(ctx, ref, sessionKey, entry); err != nil {
		oc.Log().Warn().Err(err).Str("session_key", sessionKey).Msg("session store: upsert failed")
	}
}

func (oc *AIClient) resolveSessionStoreRef(agentID string) sessionStoreRef {
	cfg := (*Config)(nil)
	if oc != nil && oc.connector != nil {
		cfg = &oc.connector.Config
	}
	storeAgentID := normalizeAgentID(agentID)
	if cfg != nil && cfg.Session != nil && normalizeSessionScope(cfg.Session.Scope) == sessionScopeGlobal {
		storeAgentID = sessionScopeGlobal
	}
	return loginScopeForClient(oc).sessionStoreRef(storeAgentID)
}
