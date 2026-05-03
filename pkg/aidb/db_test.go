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

func TestUpgradeFresh(t *testing.T) {
	ctx := context.Background()
	parentDB := setupTestDB(t)
	bridgeDB := NewChild(parentDB, dbutil.NoopLogger)
	if bridgeDB == nil {
		t.Fatalf("expected child DB")
	}

	if err := bridgeDB.Upgrade(ctx); err != nil {
		t.Fatalf("upgrade failed: %v", err)
	}

	for _, table := range []string{
		"aichats_system_events",
		"aichats_login_state",
		"aichats_portal_state",
		"sdk_conversation_state",
		"aichats_turns",
		"aichats_turn_refs",
	} {
		exists, err := bridgeDB.TableExists(ctx, table)
		if err != nil {
			t.Fatalf("check %s existence failed: %v", table, err)
		}
		if !exists {
			t.Fatalf("expected %s to exist", table)
		}
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
		"aichats_custom_agents",
		"aichats_sessions",
		"aichats_tool_approval_rules",
	} {
		exists, err := bridgeDB.TableExists(ctx, table)
		if err != nil {
			t.Fatalf("check %s absence failed: %v", table, err)
		}
		if exists {
			t.Fatalf("expected %s to be absent", table)
		}
	}
}

func TestUpgradeIdempotent(t *testing.T) {
	ctx := context.Background()
	parentDB := setupTestDB(t)
	bridgeDB := NewChild(parentDB, dbutil.NoopLogger)
	if bridgeDB == nil {
		t.Fatalf("expected child DB")
	}
	if err := bridgeDB.Upgrade(ctx); err != nil {
		t.Fatalf("upgrade failed: %v", err)
	}
	if err := bridgeDB.Upgrade(ctx); err != nil {
		t.Fatalf("second upgrade failed: %v", err)
	}
}
