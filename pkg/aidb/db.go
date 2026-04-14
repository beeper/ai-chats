package aidb

import (
	"context"
	"embed"
	"errors"
	"fmt"

	"go.mau.fi/util/dbutil"
)

const initSchemaFile = "001-init.sql"

//go:embed *.sql
var rawUpgrades embed.FS

// NewChild creates a child DB wrapper for the shared AI Chats tables.
func NewChild(base *dbutil.Database, log dbutil.DatabaseLogger) *dbutil.Database {
	if base == nil {
		return nil
	}
	if log == nil {
		log = dbutil.NoopLogger
	}
	return base.Child("", dbutil.UpgradeTable{}, log)
}

// EnsureSchema applies the canonical AI Chats schema.
func EnsureSchema(ctx context.Context, db *dbutil.Database) error {
	if db == nil {
		return errors.New("AI Chats database not initialized")
	}
	schema, err := rawUpgrades.ReadFile(initSchemaFile)
	if err != nil {
		return err
	}
	_, err = db.Exec(ctx, string(schema))
	if err != nil {
		return err
	}
	return ensureColumnSet(ctx, db, aiChatsManagedHeartbeatsColumns)
}

var aiChatsManagedHeartbeatsColumns = columnSet{
	table: "aichats_managed_heartbeats",
	columns: map[string]string{
		"last_heartbeat_session_key": "TEXT NOT NULL DEFAULT ''",
		"last_heartbeat_text":        "TEXT NOT NULL DEFAULT ''",
		"last_heartbeat_sent_at_ms":  "INTEGER NOT NULL DEFAULT 0",
	},
}

type columnSet struct {
	table   string
	columns map[string]string
}

func ensureColumnSet(ctx context.Context, db *dbutil.Database, spec columnSet) error {
	for column, definition := range spec.columns {
		exists, err := db.ColumnExists(ctx, spec.table, column)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		if _, err := db.Exec(ctx, fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", spec.table, column, definition)); err != nil {
			return err
		}
	}
	return nil
}
