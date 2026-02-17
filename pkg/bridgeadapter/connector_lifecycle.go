package bridgeadapter

import (
	"context"
	"maps"
	"sync"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

// EnsureClientMap initializes the connector client cache map when needed.
func EnsureClientMap(mu *sync.Mutex, clients *map[networkid.UserLoginID]bridgev2.NetworkAPI) {
	if mu == nil || clients == nil {
		return
	}
	mu.Lock()
	if *clients == nil {
		*clients = make(map[networkid.UserLoginID]bridgev2.NetworkAPI)
	}
	mu.Unlock()
}

// StopClients disconnects all cached clients that expose Disconnect().
func StopClients(mu *sync.Mutex, clients *map[networkid.UserLoginID]bridgev2.NetworkAPI) {
	if mu == nil || clients == nil {
		return
	}
	mu.Lock()
	cloned := maps.Clone(*clients)
	mu.Unlock()

	for _, client := range cloned {
		if dc, ok := client.(interface{ Disconnect() }); ok {
			dc.Disconnect()
		}
	}
}

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
