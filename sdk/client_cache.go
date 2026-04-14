package sdk

import (
	"context"

	"maunium.net/go/mautrix/bridgev2"
)

// PrimeUserLoginCache preloads all logins into bridgev2's in-memory user/login caches.
func PrimeUserLoginCache(ctx context.Context, br *bridgev2.Bridge) {
	if br == nil || br.DB == nil || br.DB.UserLogin == nil {
		return
	}
	userIDs, err := br.DB.UserLogin.GetAllUserIDsWithLogins(ctx)
	if err != nil {
		br.Log.Warn().Err(err).Msg("Failed to list users with logins for cache priming")
		return
	}
	for _, mxid := range userIDs {
		_, _ = br.GetUserByMXID(ctx, mxid)
	}
}
