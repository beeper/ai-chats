package ai

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"time"

	"github.com/beeper/agentremote/pkg/agents"
)

var sessionStoreLocks sync.Map

const (
	sessionScopePerSender = "per-sender"
	sessionScopeGlobal    = "global"
	defaultSessionMainKey = "main"
)

type heartbeatSessionResolution struct {
	StoreAgentID string
	SessionKey   string
	UpdatedAt    int64
}

func sessionStoreLockKey(ownerKey string, storeAgentID string, sessionKey string) string {
	agent := normalizeAgentID(storeAgentID)
	key := strings.TrimSpace(sessionKey)
	if key == "" {
		key = "main"
	}
	return ownerKey + "|" + agent + "|" + key
}

func sessionStoreLock(ownerKey string, storeAgentID string, sessionKey string) *sync.Mutex {
	key := sessionStoreLockKey(ownerKey, storeAgentID, sessionKey)
	if val, ok := sessionStoreLocks.Load(key); ok {
		return val.(*sync.Mutex)
	}
	mu := &sync.Mutex{}
	actual, _ := sessionStoreLocks.LoadOrStore(key, mu)
	return actual.(*sync.Mutex)
}

func (oc *AIClient) normalizedSessionAgentID(agentID string) string {
	resolvedAgent := normalizeAgentID(agentID)
	if resolvedAgent == "" {
		return normalizeAgentID(agents.DefaultAgentID)
	}
	return resolvedAgent
}

func (oc *AIClient) sessionScope() string {
	cfg := (*Config)(nil)
	if oc != nil && oc.connector != nil {
		cfg = &oc.connector.Config
	}
	scope := sessionScopePerSender
	if cfg != nil && cfg.Session != nil {
		if trimmed := strings.ToLower(strings.TrimSpace(cfg.Session.Scope)); trimmed == sessionScopeGlobal {
			scope = sessionScopeGlobal
		}
	}
	return scope
}

func (oc *AIClient) sessionMainKey(agentID string) string {
	resolvedAgent := oc.normalizedSessionAgentID(agentID)
	if oc.sessionScope() == sessionScopeGlobal {
		return sessionScopeGlobal
	}
	normalizedMainKey := defaultSessionMainKey
	cfg := (*Config)(nil)
	if oc != nil && oc.connector != nil {
		cfg = &oc.connector.Config
	}
	if cfg != nil && cfg.Session != nil {
		if trimmed := strings.ToLower(strings.TrimSpace(cfg.Session.MainKey)); trimmed != "" {
			normalizedMainKey = trimmed
		}
	}
	return "agent:" + resolvedAgent + ":" + normalizedMainKey
}

func (oc *AIClient) sessionStoreAgentID(agentID string) string {
	if oc.sessionScope() == sessionScopeGlobal {
		return sessionScopeGlobal
	}
	return oc.normalizedSessionAgentID(agentID)
}

func (oc *AIClient) lastRoutedSessionKey(ctx context.Context, agentID string) (string, bool) {
	if oc == nil {
		return "", false
	}
	scope := loginScopeForClient(oc)
	if scope == nil {
		return "", false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	storeAgentID := oc.sessionStoreAgentID(agentID)
	mainKey := oc.sessionMainKey(agentID)
	var sessionKey string
	err := scope.db.QueryRow(ctx, `
		SELECT session_key
		FROM `+aiSessionsTable+`
		WHERE bridge_id=$1 AND login_id=$2 AND store_agent_id=$3 AND session_key<>$4 AND session_key LIKE '!%'
		ORDER BY updated_at_ms DESC
		LIMIT 1
	`, scope.bridgeID, scope.loginID, normalizeAgentID(storeAgentID), strings.TrimSpace(mainKey)).Scan(&sessionKey)
	if err == sql.ErrNoRows {
		return "", false
	}
	if err != nil {
		oc.log.Warn().Err(err).Str("agent_id", agentID).Msg("session store: latest route lookup failed")
		return "", false
	}
	return sessionKey, true
}

func (oc *AIClient) storedSessionUpdatedAt(ctx context.Context, storeAgentID string, sessionKey string) (int64, bool) {
	if oc == nil || strings.TrimSpace(sessionKey) == "" {
		return 0, false
	}
	scope := loginScopeForClient(oc)
	if scope == nil {
		return 0, false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var updatedAt int64
	err := scope.db.QueryRow(ctx, `
		SELECT
			updated_at_ms
		FROM `+aiSessionsTable+`
		WHERE bridge_id=$1 AND login_id=$2 AND store_agent_id=$3 AND session_key=$4
	`,
		scope.bridgeID, scope.loginID, normalizeAgentID(storeAgentID), strings.TrimSpace(sessionKey),
	).Scan(&updatedAt)
	if err == sql.ErrNoRows {
		return 0, false
	}
	if err != nil {
		oc.log.Warn().Err(err).Str("session_key", sessionKey).Msg("session store: lookup failed")
		return 0, false
	}
	return updatedAt, true
}

func (oc *AIClient) saveStoredSessionUpdatedAt(ctx context.Context, storeAgentID string, sessionKey string, updatedAt int64) error {
	scope := loginScopeForClient(oc)
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
		normalizeAgentID(storeAgentID),
		strings.TrimSpace(sessionKey),
		updatedAt,
	)
	return err
}

func (oc *AIClient) touchStoredSession(ctx context.Context, storeAgentID string, sessionKey string, minUpdatedAt int64) {
	if oc == nil || strings.TrimSpace(sessionKey) == "" {
		return
	}
	scope := loginScopeForClient(oc)
	if scope == nil {
		return
	}
	lock := sessionStoreLock(scope.ownerKey(), storeAgentID, sessionKey)
	lock.Lock()
	defer lock.Unlock()

	updatedAt := time.Now().UnixMilli()
	if existingUpdatedAt, ok := oc.storedSessionUpdatedAt(ctx, storeAgentID, sessionKey); ok && existingUpdatedAt > updatedAt {
		updatedAt = existingUpdatedAt
	}
	if minUpdatedAt > updatedAt {
		updatedAt = minUpdatedAt
	}
	if err := oc.saveStoredSessionUpdatedAt(ctx, storeAgentID, sessionKey, updatedAt); err != nil {
		oc.log.Warn().Err(err).Str("session_key", sessionKey).Msg("session store: upsert failed")
	}
}
