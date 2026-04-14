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

func TestEnsureSchemaFresh(t *testing.T) {
	ctx := context.Background()
	parentDB := setupTestDB(t)
	bridgeDB := NewChild(parentDB, dbutil.NoopLogger)
	if bridgeDB == nil {
		t.Fatalf("expected child DB")
	}

	if err := EnsureSchema(ctx, bridgeDB); err != nil {
		t.Fatalf("ensure schema failed: %v", err)
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
		"aichats_login_state",
		"aichats_custom_agents",
		"aichats_portal_state",
		"aichats_sessions",
		"aichats_tool_approval_rules",
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
}

func TestEnsureSchemaIdempotent(t *testing.T) {
	ctx := context.Background()
	parentDB := setupTestDB(t)
	bridgeDB := NewChild(parentDB, dbutil.NoopLogger)
	if bridgeDB == nil {
		t.Fatalf("expected child DB")
	}
	if err := EnsureSchema(ctx, bridgeDB); err != nil {
		t.Fatalf("ensure schema failed: %v", err)
	}
	if err := EnsureSchema(ctx, bridgeDB); err != nil {
		t.Fatalf("second ensure schema failed: %v", err)
	}
}

func TestEnsureSchemaBackfillsManagedHeartbeatColumns(t *testing.T) {
	ctx := context.Background()
	parentDB := setupTestDB(t)
	bridgeDB := NewChild(parentDB, dbutil.NoopLogger)
	if bridgeDB == nil {
		t.Fatalf("expected child DB")
	}

	if _, err := bridgeDB.Exec(ctx, `
		CREATE TABLE aichats_managed_heartbeats (
			bridge_id TEXT NOT NULL,
			login_id TEXT NOT NULL,
			agent_id TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			interval_ms INTEGER NOT NULL DEFAULT 0,
			active_hours_start TEXT NOT NULL DEFAULT '',
			active_hours_end TEXT NOT NULL DEFAULT '',
			active_hours_timezone TEXT NOT NULL DEFAULT '',
			room_id TEXT NOT NULL DEFAULT '',
			revision INTEGER NOT NULL DEFAULT 1,
			next_run_at_ms INTEGER,
			pending_run_key TEXT NOT NULL DEFAULT '',
			last_run_at_ms INTEGER,
			last_result TEXT NOT NULL DEFAULT '',
			last_error TEXT NOT NULL DEFAULT '',
			PRIMARY KEY (bridge_id, login_id, agent_id)
		)
	`); err != nil {
		t.Fatalf("create legacy managed heartbeat table: %v", err)
	}

	if err := EnsureSchema(ctx, bridgeDB); err != nil {
		t.Fatalf("ensure schema failed: %v", err)
	}

	for _, column := range []string{
		"last_heartbeat_session_key",
		"last_heartbeat_text",
		"last_heartbeat_sent_at_ms",
	} {
		exists, err := bridgeDB.ColumnExists(ctx, "aichats_managed_heartbeats", column)
		if err != nil {
			t.Fatalf("check %s existence failed: %v", column, err)
		}
		if !exists {
			t.Fatalf("expected %s to exist", column)
		}
	}
}
