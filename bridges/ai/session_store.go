package ai

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type sessionEntry struct {
	SessionID           string
	UpdatedAt           int64
	LastHeartbeatText   string
	LastHeartbeatSentAt int64
	LastChannel         string
	LastTo              string
	LastAccountID       string
	LastThreadID        string
	QueueMode           string
	QueueDebounceMs     *int
	QueueCap            *int
	QueueDrop           string
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

func sessionNullInt(value *int) any {
	if value == nil {
		return nil
	}
	return int64(*value)
}

func nullableSessionInt(value sql.NullInt64) *int {
	if !value.Valid {
		return nil
	}
	v := int(value.Int64)
	return &v
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
	var (
		entry           sessionEntry
		queueDebounceMs sql.NullInt64
		queueCap        sql.NullInt64
	)
	err := scope.db.QueryRow(ctx, `
		SELECT
			session_id,
			updated_at_ms,
			last_heartbeat_text,
			last_heartbeat_sent_at_ms,
			last_channel,
			last_to,
			last_account_id,
			last_thread_id,
			queue_mode,
			queue_debounce_ms,
			queue_cap,
			queue_drop
		FROM `+aiSessionsTable+`
		WHERE bridge_id=$1 AND login_id=$2 AND store_agent_id=$3 AND session_key=$4
	`,
		scope.bridgeID, scope.loginID, normalizeAgentID(ref.AgentID), strings.TrimSpace(sessionKey),
	).Scan(
		&entry.SessionID,
		&entry.UpdatedAt,
		&entry.LastHeartbeatText,
		&entry.LastHeartbeatSentAt,
		&entry.LastChannel,
		&entry.LastTo,
		&entry.LastAccountID,
		&entry.LastThreadID,
		&entry.QueueMode,
		&queueDebounceMs,
		&queueCap,
		&entry.QueueDrop,
	)
	if err == sql.ErrNoRows {
		return sessionEntry{}, false
	}
	if err != nil {
		oc.Log().Warn().Err(err).Str("session_key", sessionKey).Msg("session store: lookup failed")
		return sessionEntry{}, false
	}
	entry.QueueDebounceMs = nullableSessionInt(queueDebounceMs)
	entry.QueueCap = nullableSessionInt(queueCap)
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
			session_id,
			updated_at_ms,
			last_heartbeat_text,
			last_heartbeat_sent_at_ms,
			last_channel,
			last_to,
			last_account_id,
			last_thread_id,
			queue_mode,
			queue_debounce_ms,
			queue_cap,
			queue_drop
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
		ON CONFLICT (bridge_id, login_id, store_agent_id, session_key) DO UPDATE SET
			session_id=excluded.session_id,
			updated_at_ms=excluded.updated_at_ms,
			last_heartbeat_text=excluded.last_heartbeat_text,
			last_heartbeat_sent_at_ms=excluded.last_heartbeat_sent_at_ms,
			last_channel=excluded.last_channel,
			last_to=excluded.last_to,
			last_account_id=excluded.last_account_id,
			last_thread_id=excluded.last_thread_id,
			queue_mode=excluded.queue_mode,
			queue_debounce_ms=excluded.queue_debounce_ms,
			queue_cap=excluded.queue_cap,
			queue_drop=excluded.queue_drop
	`,
		scope.bridgeID,
		scope.loginID,
		normalizeAgentID(ref.AgentID),
		strings.TrimSpace(sessionKey),
		entry.SessionID,
		entry.UpdatedAt,
		entry.LastHeartbeatText,
		entry.LastHeartbeatSentAt,
		entry.LastChannel,
		entry.LastTo,
		entry.LastAccountID,
		entry.LastThreadID,
		entry.QueueMode,
		sessionNullInt(entry.QueueDebounceMs),
		sessionNullInt(entry.QueueCap),
		entry.QueueDrop,
	)
	return err
}

func (oc *AIClient) updateSessionEntry(ctx context.Context, ref sessionStoreRef, sessionKey string, updater func(entry sessionEntry) sessionEntry) {
	if oc == nil || updater == nil || strings.TrimSpace(sessionKey) == "" {
		return
	}
	lock := sessionStoreLock(ref, sessionKey)
	lock.Lock()
	defer lock.Unlock()

	entry, _ := oc.getSessionEntry(ctx, ref, sessionKey)
	entry = updater(entry)
	if err := oc.upsertSessionEntry(ctx, ref, sessionKey, entry); err != nil {
		oc.Log().Warn().Err(err).Str("session_key", sessionKey).Msg("session store: upsert failed")
	}
}

func mergeSessionEntry(existing sessionEntry, patch sessionEntry) sessionEntry {
	sessionID := patch.SessionID
	if sessionID == "" {
		sessionID = existing.SessionID
	}
	if sessionID == "" {
		sessionID = uuid.NewString()
	}
	updatedAt := time.Now().UnixMilli()
	if existing.UpdatedAt > updatedAt {
		updatedAt = existing.UpdatedAt
	}
	if patch.UpdatedAt > updatedAt {
		updatedAt = patch.UpdatedAt
	}
	next := existing
	if patch.LastHeartbeatText != "" {
		next.LastHeartbeatText = patch.LastHeartbeatText
	}
	if patch.LastHeartbeatSentAt != 0 {
		next.LastHeartbeatSentAt = patch.LastHeartbeatSentAt
	}
	if patch.LastChannel != "" {
		next.LastChannel = patch.LastChannel
	}
	if patch.LastTo != "" {
		next.LastTo = patch.LastTo
	}
	if patch.LastAccountID != "" {
		next.LastAccountID = patch.LastAccountID
	}
	if patch.LastThreadID != "" {
		next.LastThreadID = patch.LastThreadID
	}
	if patch.QueueMode != "" {
		next.QueueMode = patch.QueueMode
	}
	if patch.QueueDebounceMs != nil {
		next.QueueDebounceMs = patch.QueueDebounceMs
	}
	if patch.QueueCap != nil {
		next.QueueCap = patch.QueueCap
	}
	if patch.QueueDrop != "" {
		next.QueueDrop = patch.QueueDrop
	}
	next.SessionID = sessionID
	next.UpdatedAt = updatedAt
	return next
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
