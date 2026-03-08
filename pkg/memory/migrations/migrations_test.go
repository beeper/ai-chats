package migrations

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"go.mau.fi/util/dbutil"
)

func setupTestDB(t *testing.T) *dbutil.Database {
	t.Helper()
	raw, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db, err := dbutil.NewWithDB(raw, "sqlite3")
	if err != nil {
		t.Fatalf("wrap db: %v", err)
	}
	return db
}

func TestUpgradeV1Fresh(t *testing.T) {
	ctx := context.Background()
	parentDB := setupTestDB(t)
	bridgeDB := parentDB.Child("ai_bridge_version", Table, dbutil.NoopLogger)

	if err := bridgeDB.Upgrade(ctx); err != nil {
		t.Fatalf("upgrade failed: %v", err)
	}

	var version int
	if err := bridgeDB.QueryRow(ctx, "SELECT version FROM ai_bridge_version").Scan(&version); err != nil {
		t.Fatalf("read ai_bridge_version failed: %v", err)
	}
	if version != 1 {
		t.Fatalf("expected ai_bridge_version=1, got %d", version)
	}

	for _, table := range []string{
		"ai_memory_files",
		"ai_memory_chunks",
		"ai_memory_meta",
		"ai_memory_embedding_cache",
		"ai_memory_session_state",
		"ai_memory_session_files",
		"ai_cron_jobs",
		"ai_managed_heartbeats",
		"ai_system_events",
		"ai_sessions",
		"ai_model_catalog_entries",
	} {
		exists, err := bridgeDB.TableExists(ctx, table)
		if err != nil {
			t.Fatalf("check %s existence failed: %v", table, err)
		}
		if !exists {
			t.Fatalf("expected %s to exist", table)
		}
	}
}
