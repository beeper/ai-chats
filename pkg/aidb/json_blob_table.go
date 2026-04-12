package aidb

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
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
}

// Ensure creates the table if it does not already exist.
func (t *JSONBlobTable) Ensure(ctx context.Context, db *dbutil.Database) error {
	if db == nil {
		return nil
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
	var raw string
	err := db.QueryRow(ctx, `
		SELECT state_json
		FROM `+t.TableName+`
		WHERE bridge_id=$1 AND login_id=$2 AND `+t.KeyColumn+`=$3
	`, bridgeID, loginID, key).Scan(&raw)
	if err == sql.ErrNoRows || strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	if err != nil {
		return nil, err
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
	_, err := db.Exec(ctx, `
		DELETE FROM `+t.TableName+`
		WHERE bridge_id=$1 AND login_id=$2 AND `+t.KeyColumn+`=$3
	`, bridgeID, loginID, key)
	return err
}
