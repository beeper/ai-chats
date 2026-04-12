package aidb

import (
	"context"
	"embed"
	"errors"

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

// EnsureSchema applies the canonical AI Chats schema. This bridge has never been
// released, so there is no migration or legacy compatibility path.
func EnsureSchema(ctx context.Context, db *dbutil.Database) error {
	if db == nil {
		return errors.New("AI Chats database not initialized")
	}
	schema, err := rawUpgrades.ReadFile(initSchemaFile)
	if err != nil {
		return err
	}
	_, err = db.Exec(ctx, string(schema))
	return err
}
