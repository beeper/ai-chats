package connector

import (
	"context"
	"strings"

	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix/bridgev2"
)

// purgeLoginDataBestEffort removes per-login data that lives outside bridgev2's core tables.
//
// bridgev2 will delete the user_login row (including login metadata like API keys) and, depending on
// cleanup_on_logout config, will also delete/unbridge portal rows and message history.
//
// However, this bridge stores extra per-login state (AI recall index/cache tables) that is not
// foreign-keyed to user_login and therefore will not be automatically removed.
//
// This function is intentionally best-effort: it must not block logout if cleanup fails.
func purgeLoginDataBestEffort(ctx context.Context, login *bridgev2.UserLogin) {
	if login == nil || login.Bridge == nil || login.Bridge.DB == nil {
		return
	}
	bridgeID := string(login.Bridge.DB.BridgeID)
	loginID := string(login.ID)
	if strings.TrimSpace(bridgeID) == "" || strings.TrimSpace(loginID) == "" {
		return
	}

	db := bridgeDBFromLogin(login)
	if db == nil {
		return
	}

	purgeRecallLoginDataBestEffort(ctx, login, db, bridgeID, loginID)

	// Bridge-internal KV state (scheduler state, model catalog, etc.)
	bestEffortExec(ctx, db,
		`DELETE FROM ai_bridge_state WHERE bridge_id=$1 AND login_id=$2`,
		bridgeID, loginID,
	)
}

func bestEffortExec(ctx context.Context, db *dbutil.Database, query string, args ...any) {
	if db == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	_, err := db.Exec(ctx, query, args...)
	if err == nil {
		return
	}
	// Ignore missing tables and missing virtual table modules. Older DBs or disabled features may not
	// have these tables, and some SQLite connections may not have vec0 loaded.
	// We intentionally avoid driver-specific error types here to keep postgres/sqlite builds simple.
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "no such table") ||
		strings.Contains(msg, "does not exist") ||
		strings.Contains(msg, "undefined table") ||
		strings.Contains(msg, "no such module") {
		return
	}
}
