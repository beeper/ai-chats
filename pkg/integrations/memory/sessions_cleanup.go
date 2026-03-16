package memory

import (
	"context"
	"time"
)

func (m *MemorySearchManager) purgeSessionPath(ctx context.Context, path string) {
	if path == "" {
		return
	}
	_, _ = m.db.Exec(ctx,
		`DELETE FROM aichats_memory_chunks
         WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3 AND path=$4 AND source=$5`,
		m.baseArgs(path, "sessions")...,
	)
	if m.ftsAvailable {
		_, _ = m.db.Exec(ctx,
			`DELETE FROM aichats_memory_chunks_fts
             WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3 AND path=$4 AND source=$5`,
			m.baseArgs(path, "sessions")...,
		)
	}
}

// purgeSessionData removes a session's file, state, and indexed chunks.
func (m *MemorySearchManager) purgeSessionData(ctx context.Context, sessionKey, path string) {
	m.purgeSessionPath(ctx, path)
	_, _ = m.db.Exec(ctx,
		`DELETE FROM aichats_memory_session_files
         WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3 AND session_key=$4`,
		m.baseArgs(sessionKey)...,
	)
	_, _ = m.db.Exec(ctx,
		`DELETE FROM aichats_memory_session_state
         WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3 AND session_key=$4`,
		m.baseArgs(sessionKey)...,
	)
}

// pruneExpiredSessions removes session files and their index entries that are older
// than the configured retention window. No-op if retention_days is 0 (unlimited).
func (m *MemorySearchManager) pruneExpiredSessions(ctx context.Context) {
	if m == nil {
		return
	}
	days := m.cfg.Sync.Sessions.RetentionDays
	if days <= 0 {
		return
	}
	cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour).UnixMilli()

	rows, err := m.db.Query(ctx,
		`SELECT session_key, path FROM aichats_memory_session_files
         WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3 AND updated_at < $4`,
		m.baseArgs(cutoff)...,
	)
	if err != nil {
		return
	}
	defer rows.Close()

	for rows.Next() {
		var sessionKey, path string
		if err := rows.Scan(&sessionKey, &path); err != nil {
			return
		}
		m.purgeSessionData(ctx, sessionKey, path)
	}
}
