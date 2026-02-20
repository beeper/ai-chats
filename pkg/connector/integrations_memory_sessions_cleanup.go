package connector

import (
	"context"
	"time"
)

func (m *MemorySearchManager) purgeSessionPath(ctx context.Context, path string, ops *pendingVectorOps) {
	if path == "" {
		return
	}
	if m.vectorAvailable() {
		ids := m.collectChunkIDs(ctx, path, "sessions", m.status.Model, "")
		if ops != nil {
			ops.deletes = append(ops.deletes, ids...)
		} else {
			m.deleteVectorIDs(ctx, ids)
		}
	}
	_, _ = m.db.Exec(ctx,
		`DELETE FROM ai_memory_chunks
         WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3 AND path=$4 AND source=$5`,
		m.bridgeID, m.loginID, m.agentID, path, "sessions",
	)
	if m.ftsAvailable {
		_, _ = m.db.Exec(ctx,
			`DELETE FROM ai_memory_chunks_fts
             WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3 AND path=$4 AND source=$5`,
			m.bridgeID, m.loginID, m.agentID, path, "sessions",
		)
	}
}

// pruneExpiredSessions removes session files and their index entries that are older
// than the configured retention window. No-op if retention_days is 0 (unlimited).
func (m *MemorySearchManager) pruneExpiredSessions(ctx context.Context, ops *pendingVectorOps) {
	if m == nil || m.cfg == nil {
		return
	}
	days := m.cfg.Sync.Sessions.RetentionDays
	if days <= 0 {
		return
	}
	cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour).UnixMilli()

	rows, err := m.db.Query(ctx,
		`SELECT session_key, path FROM ai_memory_session_files
         WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3 AND updated_at < $4`,
		m.bridgeID, m.loginID, m.agentID, cutoff,
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
		m.purgeSessionPath(ctx, path, ops)
		_, _ = m.db.Exec(ctx,
			`DELETE FROM ai_memory_session_files
             WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3 AND session_key=$4`,
			m.bridgeID, m.loginID, m.agentID, sessionKey,
		)
		_, _ = m.db.Exec(ctx,
			`DELETE FROM ai_memory_session_state
             WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3 AND session_key=$4`,
			m.bridgeID, m.loginID, m.agentID, sessionKey,
		)
	}
}
