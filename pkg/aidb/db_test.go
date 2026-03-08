package aidb

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
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = raw.Close() })
	db, err := dbutil.NewWithDB(raw, "sqlite3")
	if err != nil {
		t.Fatalf("wrap db: %v", err)
	}
	return db
}

func TestNewChildNilBase(t *testing.T) {
	if child := NewChild(nil, dbutil.NoopLogger); child != nil {
		t.Fatalf("expected nil child DB for nil base")
	}
}

func TestUpgradeV1Fresh(t *testing.T) {
	ctx := context.Background()
	parentDB := setupTestDB(t)
	bridgeDB := NewChild(parentDB, dbutil.NoopLogger)
	if bridgeDB == nil {
		t.Fatalf("expected child DB")
	}

	if err := Upgrade(ctx, bridgeDB, "ai_bridge", "database not initialized"); err != nil {
		t.Fatalf("upgrade failed: %v", err)
	}

	var version int
	if err := bridgeDB.QueryRow(ctx, "SELECT version FROM "+VersionTable).Scan(&version); err != nil {
		t.Fatalf("read %s failed: %v", VersionTable, err)
	}
	if version != 1 {
		t.Fatalf("expected %s=1, got %d", VersionTable, version)
	}

	for _, table := range []string{
		"ai_memory_files",
		"ai_memory_chunks",
		"ai_memory_meta",
		"ai_memory_embedding_cache",
		"ai_memory_session_state",
		"ai_memory_session_files",
		"ai_cron_jobs",
		"ai_cron_job_run_keys",
		"ai_managed_heartbeats",
		"ai_managed_heartbeat_run_keys",
		"ai_system_events",
		"ai_sessions",
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

func TestNewChildUpgrade(t *testing.T) {
	ctx := context.Background()
	parentDB := setupTestDB(t)
	bridgeDB := NewChild(parentDB, dbutil.NoopLogger)
	if bridgeDB == nil {
		t.Fatalf("expected child DB")
	}
	if err := Upgrade(ctx, bridgeDB, "ai_bridge", "database not initialized"); err != nil {
		t.Fatalf("upgrade failed: %v", err)
	}
	if err := Upgrade(ctx, bridgeDB, "ai_bridge", "database not initialized"); err != nil {
		t.Fatalf("second upgrade failed: %v", err)
	}

	var version int
	if err := bridgeDB.QueryRow(ctx, "SELECT version FROM "+VersionTable).Scan(&version); err != nil {
		t.Fatalf("read %s failed: %v", VersionTable, err)
	}
	if version != 1 {
		t.Fatalf("expected %s=1, got %d", VersionTable, version)
	}
}
