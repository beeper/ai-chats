package connector

import (
	"context"
	"errors"

	"github.com/rs/zerolog"
	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/ai-chats/pkg/shared/aihelpers"
)

// purgeLoginData removes per-login data that lives outside bridgev2's core tables.
//
// bridgev2 will delete the user_login row (including login metadata like API keys) and, depending on
// cleanup_on_logout config, will also delete/unbridge portal rows and message history.
//
// However, this bridge stores extra per-login model chat state that is not
// foreign-keyed to user_login and therefore will not be automatically removed.
func purgeLoginData(ctx context.Context, login *bridgev2.UserLogin) {
	if login == nil || login.Bridge == nil || login.Bridge.DB == nil {
		return
	}
	bridgeID := canonicalLoginBridgeID(login)
	loginID := canonicalLoginID(login)
	if loginID == "" {
		return
	}

	db := bridgeDBFromLogin(login)
	if db == nil {
		return
	}

	logger := &login.Bridge.Log
	var deleteErrs []error
	recordDelete := func(query string, args ...any) {
		if err := execDelete(ctx, db, logger, query, args...); err != nil {
			deleteErrs = append(deleteErrs, err)
		}
	}

	recordDelete(
		`DELETE FROM `+aiPortalStateTable+` WHERE bridge_id=$1 AND portal_receiver=$2`,
		bridgeID, loginID,
	)
	recordDelete(
		`DELETE FROM `+aiLoginStateTable+` WHERE bridge_id=$1 AND login_id=$2`,
		bridgeID, loginID,
	)
	if err := aihelpers.DeleteLoginConversationState(ctx, db, bridgeID, loginID); err != nil {
		deleteErrs = append(deleteErrs, err)
	}
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
		client.purgeLoginRuntimeState(ctx)
	}
}

func (oc *AIClient) purgeLoginRuntimeState(ctx context.Context) {
	if oc == nil {
		return
	}
	oc.clearLoginState(ctx)
	oc.loginConfigMu.Lock()
	oc.loginConfig = &aiLoginConfig{}
	oc.loginConfigMu.Unlock()
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
