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

// DB is the typed wrapper for the shared AI Chats database section.
type DB struct {
	*dbutil.Database
}

// New creates a typed child DB wrapper for the shared AI Chats tables.
func New(base *dbutil.Database, log dbutil.DatabaseLogger) *DB {
	db := NewChild(base, log)
	if db == nil {
		return nil
	}
	return &DB{Database: db}
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
