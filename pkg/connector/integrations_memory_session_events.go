package connector

import (
	"context"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
)

const defaultSessionSyncDebounce = 5 * time.Second

func (oc *AIClient) notifySessionMemoryChange(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	force bool,
) {
	if oc == nil || portal == nil || meta == nil {
		return
	}
	ctx = oc.backgroundContext(ctx)
	agentID := resolveAgentID(meta)
	manager, _ := oc.getMemoryManager(agentID)
	if manager == nil {
		return
	}
	sessionKey := portal.PortalKey.String()
	manager.notifySessionChanged(ctx, sessionKey, force)
}

func (m *MemorySearchManager) notifySessionChanged(ctx context.Context, sessionKey string, force bool) {
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
	if delay <= 0 {
		delay = 5 * time.Second
	}
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
	if ctx == nil {
		ctx = context.Background()
	}
	_, err := m.db.Exec(ctx,
		`INSERT INTO ai_memory_session_state
           (bridge_id, login_id, agent_id, session_key, last_rowid, pending_bytes, pending_messages, updated_at)
         VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
         ON CONFLICT (bridge_id, login_id, agent_id, session_key)
         DO UPDATE SET last_rowid=excluded.last_rowid, pending_bytes=excluded.pending_bytes,
           pending_messages=excluded.pending_messages, updated_at=excluded.updated_at`,
		m.bridgeID, m.loginID, m.agentID, sessionKey, 0, 0, 0, time.Now().UnixMilli(),
	)
	return err
}
