package textfs

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"go.mau.fi/util/dbutil"
)

func setupTextfsDB(t *testing.T) *dbutil.Database {
	t.Helper()
	raw, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db, err := dbutil.NewWithDB(raw, "sqlite3")
	if err != nil {
		t.Fatalf("wrap db: %v", err)
	}
	ctx := context.Background()
	_, err = db.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS aichats_memory_files (
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
	return db
}

func TestStoreWriteReadListDelete(t *testing.T) {
	ctx := context.Background()
	db := setupTextfsDB(t)
	store := NewStore(db, "bridge", "login", "agent")

	entry, err := store.Write(ctx, "MEMORY.md", "hello memory")
	if err != nil {
		t.Fatalf("write MEMORY.md: %v", err)
	}
	if entry.Path != "MEMORY.md" {
		t.Fatalf("unexpected path: %s", entry.Path)
	}

	if _, err := store.Write(ctx, "notes/todo.md", "checklist"); err != nil {
		t.Fatalf("write notes/todo.md: %v", err)
	}

	got, found, err := store.Read(ctx, "MEMORY.md")
	if err != nil {
		t.Fatalf("read MEMORY.md: %v", err)
	}
	if !found {
		t.Fatal("expected MEMORY.md to exist")
	}
	if got.Content != "hello memory" {
		t.Fatalf("unexpected content: %q", got.Content)
	}

	entries, err := store.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	if err := store.Delete(ctx, "MEMORY.md"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, found, err = store.Read(ctx, "MEMORY.md")
	if err != nil {
		t.Fatalf("read after delete: %v", err)
	}
	if found {
		t.Fatal("expected MEMORY.md to be deleted")
	}
}

func TestStoreWriteIfMissing(t *testing.T) {
	ctx := context.Background()
	db := setupTextfsDB(t)
	store := NewStore(db, "bridge", "login", "agent")

	if _, err := store.Write(ctx, "AGENTS.md", "original"); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}
	wrote, err := store.WriteIfMissing(ctx, "AGENTS.md", "new")
	if err != nil {
		t.Fatalf("write if missing: %v", err)
	}
	if wrote {
		t.Fatal("expected WriteIfMissing to skip existing file")
	}
	entry, found, err := store.Read(ctx, "AGENTS.md")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !found || entry.Content != "original" {
		t.Fatalf("unexpected content: %q", entry.Content)
	}

	wrote, err = store.WriteIfMissing(ctx, "SOUL.md", "persona")
	if err != nil {
		t.Fatalf("write if missing (new): %v", err)
	}
	if !wrote {
		t.Fatal("expected WriteIfMissing to create new file")
	}
	entry, found, err = store.Read(ctx, "SOUL.md")
	if err != nil {
		t.Fatalf("read new: %v", err)
	}
	if !found || entry.Content != "persona" {
		t.Fatalf("unexpected new content: %q", entry.Content)
	}
}

func TestNormalizePathAndDir(t *testing.T) {
	if _, err := NormalizePath(""); err == nil {
		t.Fatal("expected error for empty path")
	}
	if _, err := NormalizePath("../escape.md"); err == nil {
		t.Fatal("expected error for path escape")
	}
	if normalized, err := NormalizePath("file://MEMORY.md"); err != nil || normalized != "MEMORY.md" {
		t.Fatalf("unexpected normalization: %q err=%v", normalized, err)
	}
}
