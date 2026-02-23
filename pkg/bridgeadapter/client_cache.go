package bridgeadapter

import (
	"sync"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

// LoadOrCreateClient returns a cached client if reusable, otherwise creates and caches a new one.
func LoadOrCreateClient(
	mu *sync.Mutex,
	clients map[networkid.UserLoginID]bridgev2.NetworkAPI,
	loginID networkid.UserLoginID,
	reuse func(existing bridgev2.NetworkAPI) bool,
	create func() (bridgev2.NetworkAPI, error),
) (bridgev2.NetworkAPI, error) {
	if mu == nil {
		// No mutex means caller opted out of shared cache synchronization.
		return create()
	}

	mu.Lock()
	if existing := clients[loginID]; existing != nil {
		if reuse != nil && reuse(existing) {
			mu.Unlock()
			return existing, nil
		}
		delete(clients, loginID)
	}
	client, err := create()
	if err != nil {
		mu.Unlock()
		return nil, err
	}
	clients[loginID] = client
	mu.Unlock()
	return client, nil
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
