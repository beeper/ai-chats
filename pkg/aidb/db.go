package aidb

import (
	"embed"

	"go.mau.fi/util/dbutil"
)

const VersionTable = "aichats_version"

//go:embed *.sql
var rawUpgrades embed.FS

var UpgradeTable dbutil.UpgradeTable

func init() {
	UpgradeTable.RegisterFS(rawUpgrades)
}

// NewChild creates a child DB wrapper for the shared AI Chats tables.
func NewChild(base *dbutil.Database, log dbutil.DatabaseLogger) *dbutil.Database {
	if base == nil {
		return nil
	}
	if log == nil {
		log = dbutil.NoopLogger
	}
	return base.Child(VersionTable, UpgradeTable, log)
}
