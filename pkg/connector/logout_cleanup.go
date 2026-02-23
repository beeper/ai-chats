package connector

import (
	"context"
	"strings"

	"github.com/rs/zerolog"
	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix/bridgev2"
)

// purgeLoginDataBestEffort removes per-login data that lives outside bridgev2's core tables.
//
// bridgev2 will delete the user_login row (including login metadata like API keys) and, depending on
// cleanup_on_logout config, will also delete/unbridge portal rows and message history.
//
// However, this bridge stores extra per-login integration state that is not
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

	if client, ok := login.Client.(*AIClient); ok && client != nil {
		client.purgeLoginIntegrations(ctx, login, bridgeID, loginID)
	}
	var logger *zerolog.Logger
	if ctx != nil {
		logger = zerolog.Ctx(ctx)
	}

	// Bridge-internal KV state (integration state, model catalog, etc.)
	bestEffortExec(ctx, db, logger,
		`DELETE FROM ai_bridge_state WHERE bridge_id=$1 AND login_id=$2`,
		bridgeID, loginID,
	)
}

func bestEffortExec(ctx context.Context, db *dbutil.Database, logger *zerolog.Logger, query string, args ...any) {
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
	if logger != nil {
		logger.Debug().Err(err).Msg("bestEffortExec unexpected error")
	}
}
