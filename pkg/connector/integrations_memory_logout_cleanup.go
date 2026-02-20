package connector

import (
	"context"
	"strings"
	"time"

	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix/bridgev2"
)

func purgeRecallLoginDataBestEffort(
	ctx context.Context,
	login *bridgev2.UserLogin,
	db *dbutil.Database,
	bridgeID, loginID string,
) {
	if login == nil || db == nil {
		return
	}

	// Stop background recall workers and (if possible) delete vector rows via existing vector-enabled managers
	// before deleting chunk rows.
	chunkIDsByAgent := loadMemoryChunkIDsByAgentBestEffort(ctx, db, bridgeID, loginID)
	if client, ok := login.Client.(*AIClient); ok && client != nil && client.recallIntegration != nil {
		client.recallIntegration.PurgeForLogin(ctx, bridgeID, loginID, chunkIDsByAgent)
	} else {
		purgeMemoryManagersForLogin(ctx, bridgeID, loginID, chunkIDsByAgent)
	}

	// Best-effort: delete vector rows using a dedicated SQLite connection with the extension loaded.
	purgeVectorRowsBestEffort(ctx, login, bridgeID, loginID)

	purgeAIMemoryTablesBestEffort(ctx, db, bridgeID, loginID)
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
		// Missing tables are expected on old DBs or when recall is unused.
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

	// Vector table may be unavailable on some connections; attempt and ignore errors.
	bestEffortExec(ctx, db,
		`DELETE FROM ai_memory_chunks_vec WHERE id IN (
           SELECT id FROM ai_memory_chunks WHERE bridge_id=$1 AND login_id=$2
         )`,
		bridgeID, loginID,
	)

	// Core recall index/cache tables.
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

func purgeVectorRowsBestEffort(ctx context.Context, login *bridgev2.UserLogin, bridgeID, loginID string) {
	if login == nil || login.Bridge == nil || login.Bridge.DB == nil {
		return
	}
	db := bridgeDBFromLogin(login)
	if db == nil {
		return
	}
	if db.Dialect != dbutil.SQLite {
		return
	}

	// Only AI logins can have recall search vector indexing enabled.
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

	// Use a timeout to prevent indefinite blocking of the single SQLite connection.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

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

	_, _ = conn.ExecContext(ctx,
		`DELETE FROM ai_memory_chunks_vec WHERE id IN (
           SELECT id FROM ai_memory_chunks WHERE bridge_id=?1 AND login_id=?2
         )`,
		bridgeID, loginID,
	)
}
