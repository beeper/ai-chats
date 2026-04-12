package sdk

import (
	"context"
	"fmt"
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

// LoadOrCreateClient returns a cached client if reusable, otherwise creates and caches a new one.
func LoadOrCreateClient(
	mu *sync.Mutex,
	clients map[networkid.UserLoginID]bridgev2.NetworkAPI,
	loginID networkid.UserLoginID,
	reuse func(existing bridgev2.NetworkAPI) bool,
	create func() (bridgev2.NetworkAPI, error),
) (bridgev2.NetworkAPI, error) {
	if mu == nil {
		return create()
	}

	mu.Lock()
	defer mu.Unlock()
	if existing := clients[loginID]; existing != nil {
		if reuse != nil && reuse(existing) {
			return existing, nil
		}
		delete(clients, loginID)
	}
	client, err := create()
	if err != nil {
		return nil, err
	}
	clients[loginID] = client
	return client, nil
}

// LoadOrCreateTypedClient wraps LoadOrCreateClient with typed reuse/create callbacks.
func LoadOrCreateTypedClient[T bridgev2.NetworkAPI](
	mu *sync.Mutex,
	clients map[networkid.UserLoginID]bridgev2.NetworkAPI,
	login *bridgev2.UserLogin,
	reuse func(T, *bridgev2.UserLogin),
	create func() (T, error),
) (T, error) {
	var zero T
	if login == nil {
		return zero, fmt.Errorf("login is nil")
	}
	client, err := LoadOrCreateClient(
		mu,
		clients,
		login.ID,
		func(existingAPI bridgev2.NetworkAPI) bool {
			existing, ok := existingAPI.(T)
			if !ok {
				return false
			}
			if reuse != nil {
				reuse(existing, login)
			}
			login.Client = existing
			return true
		},
		func() (bridgev2.NetworkAPI, error) {
			client, err := create()
			if err != nil {
				return nil, err
			}
			login.Client = client
			return client, nil
		},
	)
	if err != nil {
		return zero, err
	}
	typed, ok := client.(T)
	if !ok {
		return zero, fmt.Errorf("unexpected client type %T", client)
	}
	return typed, nil
}

// RemoveClientFromCache removes a client from the cache by login ID.
func RemoveClientFromCache(
	mu *sync.Mutex,
	clients map[networkid.UserLoginID]bridgev2.NetworkAPI,
	loginID networkid.UserLoginID,
) {
	if mu == nil {
		return
	}
	mu.Lock()
	delete(clients, loginID)
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
		client.Disconnect()
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
