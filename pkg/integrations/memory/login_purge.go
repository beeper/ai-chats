package memory

import (
	"context"
	"strings"
	"time"

	"go.mau.fi/util/dbutil"
)

func LoadChunkIDsByAgentBestEffort(ctx context.Context, db *dbutil.Database, bridgeID, loginID string) map[string][]string {
	out := make(map[string][]string)
	if db == nil {
		return out
	}
	if ctx == nil {
		ctx = context.Background()
	}
	rows, err := db.Query(ctx,
		`SELECT id, agent_id FROM ai_memory_chunks WHERE bridge_id=$1 AND login_id=$2`,
		bridgeID, loginID,
	)
	if err != nil {
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "no such table") || strings.Contains(msg, "does not exist") || strings.Contains(msg, "undefined table") {
			return out
		}
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id, agentID string
		if err := rows.Scan(&id, &agentID); err != nil {
			continue
		}
		id = strings.TrimSpace(id)
		agentID = strings.TrimSpace(agentID)
		if id == "" {
			continue
		}
		if agentID == "" {
			agentID = "default"
		}
		out[agentID] = append(out[agentID], id)
	}
	return out
}

func PurgeTablesBestEffort(ctx context.Context, db *dbutil.Database, bridgeID, loginID string) {
	if db == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	bestEffortExec(ctx, db,
		`DELETE FROM ai_memory_chunks_fts WHERE bridge_id=$1 AND login_id=$2`,
		bridgeID, loginID,
	)
	bestEffortExec(ctx, db,
		`DELETE FROM ai_memory_session_files WHERE bridge_id=$1 AND login_id=$2`,
		bridgeID, loginID,
	)
	bestEffortExec(ctx, db,
		`DELETE FROM ai_memory_session_state WHERE bridge_id=$1 AND login_id=$2`,
		bridgeID, loginID,
	)
	bestEffortExec(ctx, db,
		`DELETE FROM ai_memory_chunks_vec WHERE id IN (
           SELECT id FROM ai_memory_chunks WHERE bridge_id=$1 AND login_id=$2
         )`,
		bridgeID, loginID,
	)
	bestEffortExec(ctx, db,
		`DELETE FROM ai_memory_embedding_cache WHERE bridge_id=$1 AND login_id=$2`,
		bridgeID, loginID,
	)
	bestEffortExec(ctx, db,
		`DELETE FROM ai_memory_chunks WHERE bridge_id=$1 AND login_id=$2`,
		bridgeID, loginID,
	)
	bestEffortExec(ctx, db,
		`DELETE FROM ai_memory_files WHERE bridge_id=$1 AND login_id=$2`,
		bridgeID, loginID,
	)
	bestEffortExec(ctx, db,
		`DELETE FROM ai_memory_meta WHERE bridge_id=$1 AND login_id=$2`,
		bridgeID, loginID,
	)
}

func PurgeVectorRowsBestEffort(ctx context.Context, db *dbutil.Database, bridgeID, loginID string, extensionPath string) {
	if db == nil || db.Dialect != dbutil.SQLite {
		return
	}
	extPath := strings.TrimSpace(extensionPath)
	if extPath == "" {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, err := db.RawDB.Conn(ctx)
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()
	_ = conn.Raw(func(driverConn any) error {
		if enabler, ok := driverConn.(purgeExtensionEnabler); ok {
			return enabler.EnableLoadExtension(true)
		}
		return nil
	})
	if _, err := conn.ExecContext(ctx, "SELECT load_extension(?)", extPath); err != nil {
		return
	}
	_ = conn.Raw(func(driverConn any) error {
		if enabler, ok := driverConn.(purgeExtensionEnabler); ok {
			return enabler.EnableLoadExtension(false)
		}
		return nil
	})
	_, _ = conn.ExecContext(ctx,
		`DELETE FROM ai_memory_chunks_vec WHERE id IN (
           SELECT id FROM ai_memory_chunks WHERE bridge_id=?1 AND login_id=?2
         )`,
		bridgeID, loginID,
	)
}

func bestEffortExec(ctx context.Context, db *dbutil.Database, query string, args ...any) {
	if db == nil {
		return
	}
	_, err := db.Exec(ctx, query, args...)
	if err == nil {
		return
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "no such table") ||
		strings.Contains(msg, "does not exist") ||
		strings.Contains(msg, "undefined table") ||
		strings.Contains(msg, "no such module") {
		return
	}
}

type purgeExtensionEnabler interface {
	EnableLoadExtension(bool) error
}
