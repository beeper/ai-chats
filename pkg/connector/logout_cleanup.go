package connector

import (
	"context"
	"strings"

	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix/bridgev2"
)

// purgeLoginDataBestEffort removes per-login data that lives outside bridgev2's core tables.
//
// bridgev2 will delete the user_login row (including login metadata like API keys) and, depending on
// cleanup_on_logout config, will also delete/unbridge portal rows and message history.
//
// However, this bridge stores extra per-login state (AI memory index/cache tables) that is not
// foreign-keyed to user_login and therefore will not be automatically removed.
//
// This function is intentionally best-effort: it must not block logout if cleanup fails.
func purgeLoginDataBestEffort(ctx context.Context, login *bridgev2.UserLogin) {
	if login == nil || login.Bridge == nil || login.Bridge.DB == nil || login.Bridge.DB.Database == nil {
		return
	}
	bridgeID := string(login.Bridge.DB.BridgeID)
	loginID := string(login.ID)
	if strings.TrimSpace(bridgeID) == "" || strings.TrimSpace(loginID) == "" {
		return
	}

	db := login.Bridge.DB.Database

	// Stop background memory workers and (if possible) delete vector rows via existing vector-enabled managers
	// before we delete the chunk rows.
	chunkIDsByAgent := loadMemoryChunkIDsByAgentBestEffort(ctx, db, bridgeID, loginID)
	purgeMemoryManagersForLogin(ctx, bridgeID, loginID, chunkIDsByAgent)

	// Best-effort: delete vector rows using a dedicated SQLite connection with the vector extension loaded.
	// This covers the case where the bridge restarted and no MemorySearchManager exists in-process to perform
	// vector cleanup using its vectorConn.
	purgeVectorRowsBestEffort(ctx, login, bridgeID, loginID)

	purgeAIMemoryTablesBestEffort(ctx, db, bridgeID, loginID)

	// Bridge-internal KV state (cron state, model catalog, etc.)
	bestEffortExec(ctx, db,
		`DELETE FROM ai_bridge_state WHERE bridge_id=$1 AND login_id=$2`,
		bridgeID, loginID,
	)
}

func loadMemoryChunkIDsByAgentBestEffort(ctx context.Context, db *dbutil.Database, bridgeID, loginID string) map[string][]string {
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
		// Missing tables are expected on old DBs or when memory is unused.
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

func purgeAIMemoryTablesBestEffort(ctx context.Context, db *dbutil.Database, bridgeID, loginID string) {
	if db == nil {
		return
	}

	// FTS table contains raw text, so delete it early.
	bestEffortExec(ctx, db,
		`DELETE FROM ai_memory_chunks_fts WHERE bridge_id=$1 AND login_id=$2`,
		bridgeID, loginID,
	)

	// Session store.
	bestEffortExec(ctx, db,
		`DELETE FROM ai_memory_session_files WHERE bridge_id=$1 AND login_id=$2`,
		bridgeID, loginID,
	)
	bestEffortExec(ctx, db,
		`DELETE FROM ai_memory_session_state WHERE bridge_id=$1 AND login_id=$2`,
		bridgeID, loginID,
	)

	// Vector table is a virtual table and may be unavailable on some connections; attempt and ignore errors.
	// Must be done before deleting ai_memory_chunks, because we use it to select the IDs.
	bestEffortExec(ctx, db,
		`DELETE FROM ai_memory_chunks_vec WHERE id IN (
           SELECT id FROM ai_memory_chunks WHERE bridge_id=$1 AND login_id=$2
         )`,
		bridgeID, loginID,
	)

	// Core memory index/cache tables.
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

func bestEffortExec(ctx context.Context, db *dbutil.Database, query string, args ...any) {
	if db == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	_, err := db.Exec(ctx, query, args...)
	if err == nil {
		return
	}
	// Ignore missing tables and missing virtual table modules. Older DBs or disabled features may not
	// have these tables, and some SQLite connections may not have vec0 loaded.
	// We intentionally avoid driver-specific error types here to keep postgres/sqlite builds simple.
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "no such table") ||
		strings.Contains(msg, "does not exist") ||
		strings.Contains(msg, "undefined table") ||
		strings.Contains(msg, "no such module") {
		return
	}
}

func purgeVectorRowsBestEffort(ctx context.Context, login *bridgev2.UserLogin, bridgeID, loginID string) {
	if login == nil || login.Bridge == nil || login.Bridge.DB == nil || login.Bridge.DB.Database == nil {
		return
	}
	db := login.Bridge.DB.Database
	if db.Dialect != dbutil.SQLite {
		return
	}

	// Only AI logins can have memory search vector indexing enabled.
	client, ok := login.Client.(*AIClient)
	if !ok || client == nil {
		return
	}

	cfg, err := resolveMemorySearchConfig(client, "")
	if err != nil || cfg == nil || !cfg.Store.Vector.Enabled {
		return
	}
	extPath := strings.TrimSpace(cfg.Store.Vector.ExtensionPath)
	if extPath == "" {
		return
	}

	if ctx == nil {
		ctx = context.Background()
	}

	conn, err := db.RawDB.Conn(ctx)
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()

	// Best-effort: enable and load the vector extension for this connection.
	_ = conn.Raw(func(driverConn any) error {
		if enabler, ok := driverConn.(loadExtensionEnabler); ok {
			return enabler.EnableLoadExtension(true)
		}
		return nil
	})
	if _, err := conn.ExecContext(ctx, "SELECT load_extension(?)", extPath); err != nil {
		return
	}
	_ = conn.Raw(func(driverConn any) error {
		if enabler, ok := driverConn.(loadExtensionEnabler); ok {
			return enabler.EnableLoadExtension(false)
		}
		return nil
	})

	// Delete vector rows for this login. This uses a subquery against ai_memory_chunks for ID selection.
	_, _ = conn.ExecContext(ctx,
		`DELETE FROM ai_memory_chunks_vec WHERE id IN (
           SELECT id FROM ai_memory_chunks WHERE bridge_id=?1 AND login_id=?2
         )`,
		bridgeID, loginID,
	)
}
