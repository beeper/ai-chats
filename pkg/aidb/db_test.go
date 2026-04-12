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

	if err := Upgrade(ctx, bridgeDB, "agentremote", "database not initialized"); err != nil {
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
		"aichats_memory_files",
		"aichats_memory_chunks",
		"aichats_memory_meta",
		"aichats_memory_embedding_cache",
		"aichats_memory_session_state",
		"aichats_memory_session_files",
		"aichats_cron_jobs",
		"aichats_cron_job_run_keys",
		"aichats_managed_heartbeats",
		"aichats_managed_heartbeat_run_keys",
		"aichats_system_events",
		"aichats_login_config",
		"aichats_portal_state",
		"aichats_sessions",
	} {
		exists, err := bridgeDB.TableExists(ctx, table)
		if err != nil {
			t.Fatalf("check %s existence failed: %v", table, err)
		}
		if !exists {
			t.Fatalf("expected %s to exist", table)
		}
	}

	for _, table := range []string{"agentremote_sessions", "agentremote_approvals"} {
		exists, err := bridgeDB.TableExists(ctx, table)
		if err != nil {
			t.Fatalf("check %s absence failed: %v", table, err)
		}
		if exists {
			t.Fatalf("expected %s to be absent", table)
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
	if err := Upgrade(ctx, bridgeDB, "agentremote", "database not initialized"); err != nil {
		t.Fatalf("upgrade failed: %v", err)
	}
	if err := Upgrade(ctx, bridgeDB, "agentremote", "database not initialized"); err != nil {
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
