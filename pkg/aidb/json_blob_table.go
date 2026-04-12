package aidb

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"go.mau.fi/util/dbutil"
)

// JSONBlobTable provides ensureTable / load / save / delete CRUD for a simple
// three-key (bridge_id, login_id, <key_column>) table that stores its payload
// as a single JSON text column. This pattern is duplicated across the ai, codex,
// and openclaw bridge packages.
type JSONBlobTable struct {
	TableName string // e.g. "aichats_portal_state"
	KeyColumn string // third key column, e.g. "portal_id" or "portal_key"

	validateOnce sync.Once
	validateErr  error
}

var jsonBlobTableIdent = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func (t *JSONBlobTable) validateIdentifiers() error {
	if t == nil {
		return fmt.Errorf("json blob table is nil")
	}
	t.validateOnce.Do(func() {
		if !jsonBlobTableIdent.MatchString(t.TableName) {
			t.validateErr = fmt.Errorf("invalid table name %q", t.TableName)
			return
		}
		if !jsonBlobTableIdent.MatchString(t.KeyColumn) {
			t.validateErr = fmt.Errorf("invalid key column %q", t.KeyColumn)
		}
	})
	return t.validateErr
}

// Ensure creates the table if it does not already exist.
func (t *JSONBlobTable) Ensure(ctx context.Context, db *dbutil.Database) error {
	if db == nil {
		return nil
	}
	if err := t.validateIdentifiers(); err != nil {
		return err
	}
	_, err := db.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS `+t.TableName+` (
			bridge_id TEXT NOT NULL,
			login_id TEXT NOT NULL,
			`+t.KeyColumn+` TEXT NOT NULL,
			state_json TEXT NOT NULL DEFAULT '{}',
			updated_at_ms INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (bridge_id, login_id, `+t.KeyColumn+`)
		)
	`)
	return err
}

// Load reads and unmarshals the JSON blob for the given key triple.
// Returns (nil, nil) when no row exists or the stored JSON is empty.
func Load[T any](t *JSONBlobTable, ctx context.Context, db *dbutil.Database, bridgeID, loginID, key string) (*T, error) {
	if db == nil {
		return nil, nil
	}
	if err := t.validateIdentifiers(); err != nil {
		return nil, err
	}
	var raw string
	err := db.QueryRow(ctx, `
		SELECT state_json
		FROM `+t.TableName+`
		WHERE bridge_id=$1 AND login_id=$2 AND `+t.KeyColumn+`=$3
	`, bridgeID, loginID, key).Scan(&raw)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var out T
	if err = json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Save marshals the value to JSON and upserts it into the table.
func Save[T any](t *JSONBlobTable, ctx context.Context, db *dbutil.Database, bridgeID, loginID, key string, value *T) error {
	if db == nil || value == nil {
		return nil
	}
	if err := t.validateIdentifiers(); err != nil {
		return err
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = db.Exec(ctx, `
		INSERT INTO `+t.TableName+` (
			bridge_id, login_id, `+t.KeyColumn+`, state_json, updated_at_ms
		) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (bridge_id, login_id, `+t.KeyColumn+`) DO UPDATE SET
			state_json=excluded.state_json,
			updated_at_ms=excluded.updated_at_ms
	`, bridgeID, loginID, key, string(payload), time.Now().UnixMilli())
	return err
}

// Delete removes the row for the given key triple.
func (t *JSONBlobTable) Delete(ctx context.Context, db *dbutil.Database, bridgeID, loginID, key string) error {
	if db == nil {
		return nil
	}
	if err := t.validateIdentifiers(); err != nil {
		return err
	}
	_, err := db.Exec(ctx, `
		DELETE FROM `+t.TableName+`
		WHERE bridge_id=$1 AND login_id=$2 AND `+t.KeyColumn+`=$3
	`, bridgeID, loginID, key)
	return err
}

// BlobScope bundles a JSONBlobTable reference with the three-key coordinates
// needed for every CRUD call. Bridge packages build a BlobScope from their
// own portal/login objects and then use the scoped helpers below.
type BlobScope struct {
	Table    *JSONBlobTable
	DB       *dbutil.Database
	BridgeID string
	LoginID  string
	Key      string
}

// LoadScoped ensures the table exists and loads the JSON blob for the scope's key triple.
// Returns (nil, nil) when no row exists, matching Load semantics.
func LoadScoped[T any](ctx context.Context, scope *BlobScope) (*T, error) {
	if scope == nil {
		return nil, nil
	}
	if err := scope.Table.Ensure(ctx, scope.DB); err != nil {
		return nil, err
	}
	return Load[T](scope.Table, ctx, scope.DB, scope.BridgeID, scope.LoginID, scope.Key)
}

// LoadScopedOrNew ensures the table exists and loads the JSON blob, returning
// a zero-value T when no row exists. This is the common "load or default" pattern.
func LoadScopedOrNew[T any](ctx context.Context, scope *BlobScope) (*T, error) {
	result, err := LoadScoped[T](ctx, scope)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return new(T), nil
	}
	return result, nil
}

// SaveScoped ensures the table exists and upserts the value at the scope's key triple.
func SaveScoped[T any](ctx context.Context, scope *BlobScope, value *T) error {
	if scope == nil || value == nil {
		return nil
	}
	if err := scope.Table.Ensure(ctx, scope.DB); err != nil {
		return err
	}
	return Save(scope.Table, ctx, scope.DB, scope.BridgeID, scope.LoginID, scope.Key, value)
}

// DeleteScoped ensures the table exists and removes the row at the scope's key triple.
func DeleteScoped(ctx context.Context, scope *BlobScope) error {
	if scope == nil {
		return nil
	}
	if err := scope.Table.Ensure(ctx, scope.DB); err != nil {
		return err
	}
	return scope.Table.Delete(ctx, scope.DB, scope.BridgeID, scope.LoginID, scope.Key)
}
