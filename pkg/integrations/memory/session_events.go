package memory

import (
	"context"
	"strings"
	"time"
)

const defaultSessionSyncDebounce = 5 * time.Second

func (m *MemorySearchManager) NotifySessionChanged(ctx context.Context, sessionKey string, force bool) {
	if m == nil || m.cfg == nil {
		return
	}
	if !m.cfg.Experimental.SessionMemory || !hasSource(m.cfg.Sources, "sessions") {
		return
	}
	key := strings.TrimSpace(sessionKey)
	if force && key != "" {
		_ = m.resetSessionState(ctx, key)
	}
	// TryLock: if sync() holds mu we skip setting sessionsDirty — the scheduled
	// sync will pick up session changes regardless.
	if m.mu.TryLock() {
		m.sessionsDirty = true
		m.mu.Unlock()
	}
	m.scheduleSessionSync(key)
}

func (m *MemorySearchManager) scheduleSessionSync(sessionKey string) {
	if m == nil {
		return
	}
	key := strings.TrimSpace(sessionKey)
	delay := defaultSessionSyncDebounce
	m.mu.Lock()
	m.sessionWatchKey = key
	if m.sessionWatchTimer != nil {
		m.sessionWatchTimer.Stop()
	}
	m.sessionWatchTimer = time.AfterFunc(delay, func() {
		m.mu.Lock()
		syncKey := m.sessionWatchKey
		m.sessionWatchTimer = nil
		m.mu.Unlock()
		if err := m.sync(context.Background(), syncKey, false); err != nil {
			m.log.Warn().Err(err).Msg("memory sync failed on session change")
		}
	})
	m.mu.Unlock()
}

func (m *MemorySearchManager) resetSessionState(ctx context.Context, sessionKey string) error {
	if m == nil || sessionKey == "" {
		return nil
	}
	_, err := m.db.Exec(ctx,
		`INSERT INTO aichats_memory_session_state
           (bridge_id, login_id, agent_id, session_key, content_hash, updated_at)
         VALUES ($1, $2, $3, $4, $5, $6)
         ON CONFLICT (bridge_id, login_id, agent_id, session_key)
         DO UPDATE SET content_hash=excluded.content_hash, updated_at=excluded.updated_at`,
		m.baseArgs(sessionKey, "", time.Now().UnixMilli())...,
	)
	return err
}
