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

func TestStoreDirEntries(t *testing.T) {
	ctx := context.Background()
	db := setupTextfsDB(t)
	store := NewStore(db, "bridge", "login", "agent")

	if _, err := store.Write(ctx, "memory/2026-01-01.md", "one"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := store.Write(ctx, "memory/2026-01-02.md", "two"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := store.Write(ctx, "notes/a.md", "note"); err != nil {
		t.Fatalf("write: %v", err)
	}

	entries, err := store.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	rootEntries, _ := store.DirEntries(entries, "")
	if len(rootEntries) != 3 {
		t.Fatalf("expected 3 root entries, got %d", len(rootEntries))
	}
	foundMemory := false
	foundNotes := false
	foundWorkspace := false
	for _, entry := range rootEntries {
		if entry == "memory/" {
			foundMemory = true
		}
		if entry == "notes/" {
			foundNotes = true
		}
		if entry == "workspace/" {
			foundWorkspace = true
		}
	}
	if !foundMemory || !foundNotes || !foundWorkspace {
		t.Fatalf("expected memory/, notes/, and workspace/ in root entries: %v", rootEntries)
	}

	memEntries, _ := store.DirEntries(entries, "memory")
	if len(memEntries) != 2 {
		t.Fatalf("expected 2 memory entries, got %d", len(memEntries))
	}
}

func TestStoreListWithPrefixNormalizesDir(t *testing.T) {
	ctx := context.Background()
	db := setupTextfsDB(t)
	store := NewStore(db, "bridge", "login", "agent")

	if _, err := store.Write(ctx, "memory/2026-02-07.md", "hello"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := store.Write(ctx, "notes/todo.md", "todo"); err != nil {
		t.Fatalf("write: %v", err)
	}

	entries, err := store.ListWithPrefix(ctx, "/memory/")
	if err != nil {
		t.Fatalf("list with prefix: %v", err)
	}
	if len(entries) != 1 || entries[0].Path != "memory/2026-02-07.md" {
		t.Fatalf("unexpected entries for /memory/: %+v", entries)
	}

	entries, err = store.ListWithPrefix(ctx, "./notes")
	if err != nil {
		t.Fatalf("list with prefix: %v", err)
	}
	if len(entries) != 1 || entries[0].Path != "notes/todo.md" {
		t.Fatalf("unexpected entries for ./notes: %+v", entries)
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
	if dir, err := NormalizeDir("/"); err != nil || dir != "" {
		t.Fatalf("unexpected dir normalization: %q err=%v", dir, err)
	}
}
