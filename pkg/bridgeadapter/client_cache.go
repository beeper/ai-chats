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
	mu.Unlock()

	client, err := create()
	if err != nil {
		return nil, err
	}

	mu.Lock()
	clients[loginID] = client
	mu.Unlock()
	return client, nil
}
