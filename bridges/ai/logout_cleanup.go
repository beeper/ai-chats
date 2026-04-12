package ai

import (
	"context"
	"strings"

	"github.com/rs/zerolog"
	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix/bridgev2"
)

// purgeLoginData removes per-login data that lives outside bridgev2's core tables.
//
// bridgev2 will delete the user_login row (including login metadata like API keys) and, depending on
// cleanup_on_logout config, will also delete/unbridge portal rows and message history.
//
// However, this bridge stores extra per-login integration state that is not
// foreign-keyed to user_login and therefore will not be automatically removed.
//
func purgeLoginData(ctx context.Context, login *bridgev2.UserLogin) {
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

	execDelete(ctx, db, logger,
		`DELETE FROM `+aiSessionsTable+` WHERE bridge_id=$1 AND login_id=$2`,
		bridgeID, loginID,
	)
	execDelete(ctx, db, logger,
		`DELETE FROM aichats_cron_jobs WHERE bridge_id=$1 AND login_id=$2`,
		bridgeID, loginID,
	)
	execDelete(ctx, db, logger,
		`DELETE FROM aichats_managed_heartbeats WHERE bridge_id=$1 AND login_id=$2`,
		bridgeID, loginID,
	)
	execDelete(ctx, db, logger,
		`DELETE FROM aichats_system_events WHERE bridge_id=$1 AND login_id=$2`,
		bridgeID, loginID,
	)
	execDelete(ctx, db, logger,
		`DELETE FROM aichats_internal_messages WHERE bridge_id=$1 AND login_id=$2`,
		bridgeID, loginID,
	)
	execDelete(ctx, db, logger,
		`DELETE FROM aichats_tool_approval_rules WHERE bridge_id=$1 AND login_id=$2`,
		bridgeID, loginID,
	)
	execDelete(ctx, db, logger,
		`DELETE FROM aichats_login_state WHERE bridge_id=$1 AND login_id=$2`,
		bridgeID, loginID,
	)
	execDelete(ctx, db, logger,
		`DELETE FROM `+aiLoginConfigTable+` WHERE bridge_id=$1 AND login_id=$2`,
		bridgeID, loginID,
	)
	execDelete(ctx, db, logger,
		`DELETE FROM `+aiCustomAgentsTable+` WHERE bridge_id=$1 AND login_id=$2`,
		bridgeID, loginID,
	)
	if client, ok := login.Client.(*AIClient); ok && client != nil {
		client.clearLoginState(ctx)
		client.loginConfigMu.Lock()
		client.loginConfig = &aiLoginConfig{}
		client.loginConfigMu.Unlock()
	}
}

func execDelete(ctx context.Context, db *dbutil.Database, logger *zerolog.Logger, query string, args ...any) {
	if db == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	_, err := db.Exec(ctx, query, args...)
	if err != nil && logger != nil {
		logger.Warn().Err(err).Msg("failed to delete login-owned AI state")
	}
}

func bestEffortExec(ctx context.Context, db *dbutil.Database, logger *zerolog.Logger, query string, args ...any) {
	execDelete(ctx, db, logger, query, args...)
}
