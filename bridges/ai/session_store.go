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

type sessionRouting struct {
	AgentID      string
	StoreAgentID string
	MainKey      string
	Scope        string
}

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

func (oc *AIClient) resolveSessionRouting(agentID string) sessionRouting {
	cfg := (*Config)(nil)
	if oc != nil && oc.connector != nil {
		cfg = &oc.connector.Config
	}
	resolvedAgent := normalizeAgentID(agentID)
	if resolvedAgent == "" {
		resolvedAgent = normalizeAgentID(agents.DefaultAgentID)
	}
	scope := sessionScopePerSender
	if cfg != nil && cfg.Session != nil {
		if trimmed := strings.ToLower(strings.TrimSpace(cfg.Session.Scope)); trimmed == sessionScopeGlobal {
			scope = sessionScopeGlobal
		}
	}
	normalizedMainKey := defaultSessionMainKey
	if cfg != nil && cfg.Session != nil {
		if trimmed := strings.ToLower(strings.TrimSpace(cfg.Session.MainKey)); trimmed != "" {
			normalizedMainKey = trimmed
		}
	}
	mainSessionKey := "agent:" + resolvedAgent + ":" + normalizedMainKey
	storeAgentID := resolvedAgent
	if scope == sessionScopeGlobal {
		mainSessionKey = sessionScopeGlobal
		storeAgentID = sessionScopeGlobal
	}
	return sessionRouting{
		AgentID:      resolvedAgent,
		StoreAgentID: storeAgentID,
		MainKey:      mainSessionKey,
		Scope:        scope,
	}
}

func (oc *AIClient) resolveHeartbeatSession(agentID string, heartbeat *HeartbeatConfig) heartbeatSessionResolution {
	routing := oc.resolveSessionRouting(agentID)
	lookup := func(key string) (int64, bool) {
		return oc.loadStoredSessionUpdatedAt(context.Background(), routing.StoreAgentID, key)
	}
	if routing.Scope == sessionScopeGlobal {
		return heartbeatSessionResolution{StoreAgentID: routing.StoreAgentID, SessionKey: routing.MainKey}
	}

	trimmed := ""
	if heartbeat != nil && heartbeat.Session != nil {
		trimmed = strings.TrimSpace(*heartbeat.Session)
	}
	isMainAlias := func(raw string) bool {
		candidate := strings.TrimSpace(raw)
		if candidate == "" {
			return false
		}
		normalizedMain := strings.ToLower(strings.TrimSpace(routing.MainKey))
		if normalizedMain == "" {
			normalizedMain = defaultSessionMainKey
		}
		agentMainAlias := "agent:" + routing.AgentID + ":" + defaultSessionMainKey
		return strings.EqualFold(candidate, defaultSessionMainKey) ||
			strings.EqualFold(candidate, sessionScopeGlobal) ||
			strings.EqualFold(candidate, normalizedMain) ||
			strings.EqualFold(candidate, routing.MainKey) ||
			strings.EqualFold(candidate, agentMainAlias)
	}
	sessionKey := routing.MainKey
	if routing.Scope != sessionScopeGlobal && !isMainAlias(trimmed) {
		if strings.HasPrefix(trimmed, "!") {
			sessionKey = trimmed
		} else {
			candidate := strings.ToLower(trimmed)
			if candidate == "" || strings.EqualFold(candidate, defaultSessionMainKey) {
				candidate = routing.MainKey
			} else if !strings.HasPrefix(candidate, "agent:") {
				candidate = "agent:" + routing.AgentID + ":" + candidate
			}
			if strings.HasPrefix(candidate, "agent:"+routing.AgentID+":") && !isMainAlias(candidate) {
				sessionKey = candidate
			}
		}
	}
	if sessionKey == routing.MainKey {
		return heartbeatSessionResolution{StoreAgentID: routing.StoreAgentID, SessionKey: sessionKey}
	}
	if updatedAt, ok := lookup(sessionKey); ok {
		return heartbeatSessionResolution{StoreAgentID: routing.StoreAgentID, SessionKey: sessionKey, UpdatedAt: updatedAt}
	}
	return heartbeatSessionResolution{StoreAgentID: routing.StoreAgentID, SessionKey: sessionKey}
}

func (oc *AIClient) loadSessionUpdatedAt(ctx context.Context, agentID string, sessionKey string) (int64, bool) {
	return oc.loadStoredSessionUpdatedAt(ctx, oc.resolveSessionRouting(agentID).StoreAgentID, sessionKey)
}

func (oc *AIClient) loadStoredSessionUpdatedAt(ctx context.Context, storeAgentID string, sessionKey string) (int64, bool) {
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

func (oc *AIClient) storeSessionUpdatedAt(ctx context.Context, storeAgentID string, sessionKey string, updatedAt int64) error {
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

func (oc *AIClient) updateSessionTimestamp(ctx context.Context, storeAgentID string, sessionKey string, minUpdatedAt int64) {
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
	if existingUpdatedAt, ok := oc.loadStoredSessionUpdatedAt(ctx, storeAgentID, sessionKey); ok && existingUpdatedAt > updatedAt {
		updatedAt = existingUpdatedAt
	}
	if minUpdatedAt > updatedAt {
		updatedAt = minUpdatedAt
	}
	if err := oc.storeSessionUpdatedAt(ctx, storeAgentID, sessionKey, updatedAt); err != nil {
		oc.log.Warn().Err(err).Str("session_key", sessionKey).Msg("session store: upsert failed")
	}
}
