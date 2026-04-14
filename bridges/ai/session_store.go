package ai

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"time"
)

var sessionStoreLocks sync.Map

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

func (oc *AIClient) loadSessionUpdatedAt(ctx context.Context, storeAgentID string, sessionKey string) (int64, bool) {
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
	if existingUpdatedAt, ok := oc.loadSessionUpdatedAt(ctx, storeAgentID, sessionKey); ok && existingUpdatedAt > updatedAt {
		updatedAt = existingUpdatedAt
	}
	if minUpdatedAt > updatedAt {
		updatedAt = minUpdatedAt
	}
	if err := oc.storeSessionUpdatedAt(ctx, storeAgentID, sessionKey, updatedAt); err != nil {
		oc.log.Warn().Err(err).Str("session_key", sessionKey).Msg("session store: upsert failed")
	}
}

func (oc *AIClient) resolveSessionStoreAgentID(agentID string) string {
	return oc.resolveSessionRouting(agentID).StoreAgentID
}
