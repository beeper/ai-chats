package connector

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/rs/zerolog"
	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

func setupVfsTimeoutDB(t *testing.T, dsn string) *database.Database {
	t.Helper()
	raw, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	raw.SetMaxOpenConns(1)
	db, err := dbutil.NewWithDB(raw, "sqlite3")
	if err != nil {
		t.Fatalf("wrap db: %v", err)
	}
	ctx := context.Background()
	_, err = db.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS ai_memory_files (
			bridge_id TEXT NOT NULL,
			login_id TEXT NOT NULL,
			agent_id TEXT NOT NULL,
			path TEXT NOT NULL,
			source TEXT NOT NULL DEFAULT 'memory',
			content TEXT NOT NULL,
			hash TEXT NOT NULL,
			updated_at INTEGER NOT NULL,
			PRIMARY KEY (bridge_id, login_id, agent_id, path)
		);
	`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}
	return database.New(networkid.BridgeID("bridge"), database.MetaTypes{}, db)
}

func TestVFSWriteReturnsWithinTimeoutUnderLock(t *testing.T) {
	dsn := "file:vfs_timeout?mode=memory&cache=shared&_busy_timeout=5000"
	db := setupVfsTimeoutDB(t, dsn)

	lockDB, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("open lock sqlite: %v", err)
	}
	lockDB.SetMaxOpenConns(1)
	if _, err := lockDB.Exec("BEGIN EXCLUSIVE"); err != nil {
		lockDB.Close()
		t.Fatalf("begin exclusive: %v", err)
	}
	defer func() {
		_, _ = lockDB.Exec("ROLLBACK")
		_ = lockDB.Close()
	}()

	bridge := &bridgev2.Bridge{DB: db}
	login := &database.UserLogin{ID: networkid.UserLoginID("login")}
	userLogin := &bridgev2.UserLogin{UserLogin: login, Bridge: bridge, Log: zerolog.Nop()}
	oc := &AIClient{
		UserLogin: userLogin,
		connector: &OpenAIConnector{Config: Config{}},
		log:       zerolog.Nop(),
	}

	portal := &bridgev2.Portal{Portal: &database.Portal{Metadata: &PortalMetadata{}}}
	btc := &BridgeToolContext{
		Client: oc,
		Portal: portal,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	ctx = WithBridgeToolContext(ctx, btc)

	start := time.Now()
	_, err = executeWriteFile(ctx, map[string]any{
		"path":    "memory/test.md",
		"content": "hi",
	})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatalf("expected write to fail under lock")
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("expected write to return quickly, took %s", elapsed)
	}
}
