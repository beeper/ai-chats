package bridgeadapter

import (
	"context"
	"errors"

	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/ai-bridge/pkg/memory/migrations"
)

// MakeMemoryChildDB creates a child DB using the shared memory migration table.
func MakeMemoryChildDB(base *dbutil.Database, versionTable string, log dbutil.DatabaseLogger) *dbutil.Database {
	if base == nil {
		return nil
	}
	if log == nil {
		log = dbutil.NoopLogger
	}
	return base.Child(versionTable, migrations.Table, log)
}

// UpgradeChildDB validates and upgrades a child DB, wrapping errors as DBUpgradeError.
func UpgradeChildDB(ctx context.Context, db *dbutil.Database, section, nilMessage string) error {
	if db == nil {
		if nilMessage == "" {
			nilMessage = "database not initialized"
		}
		return bridgev2.DBUpgradeError{
			Err:     errors.New(nilMessage),
			Section: section,
		}
	}
	if err := db.Upgrade(ctx); err != nil {
		return bridgev2.DBUpgradeError{
			Err:     err,
			Section: section,
		}
	}
	return nil
}
