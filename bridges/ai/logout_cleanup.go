package ai

import (
	"context"
	"errors"
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
	logger := &login.Bridge.Log
	var deleteErrs []error
	recordDelete := func(query string, args ...any) {
		if err := execDelete(ctx, db, logger, query, args...); err != nil {
			deleteErrs = append(deleteErrs, err)
		}
	}

	recordDelete(
		`DELETE FROM `+aiSessionsTable+` WHERE bridge_id=$1 AND login_id=$2`,
		bridgeID, loginID,
	)
	recordDelete(
		`DELETE FROM `+aiCronJobsTable+` WHERE bridge_id=$1 AND login_id=$2`,
		bridgeID, loginID,
	)
	recordDelete(
		`DELETE FROM `+aiCronJobRunKeysTable+` WHERE bridge_id=$1 AND login_id=$2`,
		bridgeID, loginID,
	)
	recordDelete(
		`DELETE FROM `+aiManagedHeartbeatsTable+` WHERE bridge_id=$1 AND login_id=$2`,
		bridgeID, loginID,
	)
	recordDelete(
		`DELETE FROM `+aiHeartbeatRunKeysTable+` WHERE bridge_id=$1 AND login_id=$2`,
		bridgeID, loginID,
	)
	recordDelete(
		`DELETE FROM `+aiSystemEventsTable+` WHERE bridge_id=$1 AND login_id=$2`,
		bridgeID, loginID,
	)
	recordDelete(
		`DELETE FROM `+aiPortalStateTable+` WHERE bridge_id=$1 AND portal_receiver=$2`,
		bridgeID, loginID,
	)
	recordDelete(
		`DELETE FROM `+aiToolApprovalRulesTable+` WHERE bridge_id=$1 AND login_id=$2`,
		bridgeID, loginID,
	)
	recordDelete(
		`DELETE FROM `+aiLoginStateTable+` WHERE bridge_id=$1 AND login_id=$2`,
		bridgeID, loginID,
	)
	recordDelete(
		`DELETE FROM `+aiLoginConfigTable+` WHERE bridge_id=$1 AND login_id=$2`,
		bridgeID, loginID,
	)
	recordDelete(
		`DELETE FROM `+aiCustomAgentsTable+` WHERE bridge_id=$1 AND login_id=$2`,
		bridgeID, loginID,
	)
	recordDelete(
		`DELETE FROM `+aiTurnRefsTable+` WHERE bridge_id=$1 AND portal_receiver=$2`,
		bridgeID, loginID,
	)
	recordDelete(
		`DELETE FROM `+aiTurnsTable+` WHERE bridge_id=$1 AND portal_receiver=$2`,
		bridgeID, loginID,
	)
	if err := errors.Join(deleteErrs...); err != nil {
		logger.Warn().Err(err).Str("login_id", loginID).Msg("failed to purge some login-owned AI state")
	}
	if client, ok := login.Client.(*AIClient); ok && client != nil {
		client.clearLoginState(ctx)
		client.loginConfigMu.Lock()
		client.loginConfig = &aiLoginConfig{}
		client.loginConfigMu.Unlock()
	}
}

func execDelete(ctx context.Context, db *dbutil.Database, logger *zerolog.Logger, query string, args ...any) error {
	if db == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	_, err := db.Exec(ctx, query, args...)
	if err != nil && logger != nil {
		logger.Warn().Err(err).Msg("failed to delete login-owned AI state")
	}
	return err
}
