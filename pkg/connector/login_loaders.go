package connector

import (
	"strings"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/ai-bridge/pkg/shared/stringutil"
)

// storeOrReuseClient attempts to store a newly created client in the cache.
// If another goroutine already stored a client for this login, the new client
// is discarded and the cached one is reused instead. The winning client is
// wired into login.Client and scheduled for bootstrap. Must be called without
// oc.clientsMu held.
func (oc *OpenAIConnector) storeOrReuseClient(login *bridgev2.UserLogin, client *AIClient) {
	oc.clientsMu.Lock()
	if cachedAPI := oc.clients[login.ID]; cachedAPI != nil {
		if cached, ok := cachedAPI.(*AIClient); ok && cached != nil {
			client.Disconnect()
			cached.UserLogin = login
			login.Client = cached
			oc.clientsMu.Unlock()
			cached.scheduleBootstrap()
			return
		}
	}
	oc.clients[login.ID] = client
	oc.clientsMu.Unlock()
	login.Client = client
	client.scheduleBootstrap()
}

func (oc *OpenAIConnector) rebuildClient(login *bridgev2.UserLogin, key string) (*AIClient, error) {
	client, err := newAIClient(login, oc, key)
	if err != nil {
		return nil, err
	}
	oc.storeOrReuseClient(login, client)
	return client, nil
}

func (oc *OpenAIConnector) existingClientNeedsRebuild(existing *AIClient, meta *UserLoginMetadata, key string) bool {
	existingMeta := loginMetadata(existing.UserLogin)
	return existing.apiKey != key ||
		!strings.EqualFold(strings.TrimSpace(existingMeta.Provider), strings.TrimSpace(meta.Provider)) ||
		stringutil.NormalizeBaseURL(existingMeta.BaseURL) != stringutil.NormalizeBaseURL(meta.BaseURL)
}

func (oc *OpenAIConnector) loadAIUserLogin(login *bridgev2.UserLogin, meta *UserLoginMetadata) error {
	key := strings.TrimSpace(oc.resolveProviderAPIKey(meta))
	if key == "" {
		login.Client = newBrokenLoginClient(login, "No API key available for this login. Sign in again or remove this account.")
		return nil
	}

	oc.clientsMu.Lock()
	existingAPI := oc.clients[login.ID]
	if existingAPI == nil {
		oc.clientsMu.Unlock()
		return oc.buildNewClient(login, key)
	}

	existing, ok := existingAPI.(*AIClient)
	if !ok || existing == nil {
		// Type mismatch: rebuild.
		delete(oc.clients, login.ID)
		oc.clientsMu.Unlock()
		return oc.buildNewClient(login, key)
	}

	if oc.existingClientNeedsRebuild(existing, meta, key) {
		oc.clientsMu.Unlock()
		if _, err := oc.rebuildClient(login, key); err != nil {
			// Keep existing client on rebuild failure so it stays cached/deletable.
			oc.reuseExistingClient(login, existing)
		}
		return nil
	}

	// Provider settings unchanged: keep using the existing client.
	existing.UserLogin = login
	login.Client = existing
	oc.clientsMu.Unlock()
	existing.scheduleBootstrap()
	return nil
}

func (oc *OpenAIConnector) buildNewClient(login *bridgev2.UserLogin, key string) error {
	client, err := newAIClient(login, oc, key)
	if err != nil {
		login.Client = newBrokenLoginClient(login, "Couldn't initialize this login. Remove and re-add the account.")
		return nil
	}
	oc.storeOrReuseClient(login, client)
	return nil
}

func (oc *OpenAIConnector) reuseExistingClient(login *bridgev2.UserLogin, existing *AIClient) {
	oc.clientsMu.Lock()
	existing.UserLogin = login
	login.Client = existing
	oc.clientsMu.Unlock()
}

