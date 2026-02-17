package connector

import (
	"strings"

	"maunium.net/go/mautrix/bridgev2"
)

func (oc *OpenAIConnector) loadAIUserLogin(login *bridgev2.UserLogin, meta *UserLoginMetadata) error {
	key := strings.TrimSpace(oc.resolveProviderAPIKey(meta))
	if key == "" {
		login.Client = newBrokenLoginClient(login, "No API key available for this login. Sign in again or remove this account.")
		return nil
	}
	oc.clientsMu.Lock()
	if existingAPI := oc.clients[login.ID]; existingAPI != nil {
		existing, ok := existingAPI.(*AIClient)
		if !ok || existing == nil {
			// Type mismatch: rebuild.
			delete(oc.clients, login.ID)
			oc.clientsMu.Unlock()
			client, err := newAIClient(login, oc, key)
			if err != nil {
				login.Client = newBrokenLoginClient(login, "Couldn't initialize this login. Remove and re-add the account.")
				return nil
			}
			oc.clientsMu.Lock()
			oc.clients[login.ID] = client
			oc.clientsMu.Unlock()
			login.Client = client
			client.scheduleBootstrap()
			return nil
		}

		existingMeta := loginMetadata(existing.UserLogin)
		existingProvider := strings.TrimSpace(existingMeta.Provider)
		existingBaseURL := strings.TrimRight(strings.TrimSpace(existingMeta.BaseURL), "/")
		needsRebuild := existing.apiKey != key ||
			!strings.EqualFold(existingProvider, strings.TrimSpace(meta.Provider)) ||
			existingBaseURL != strings.TrimRight(strings.TrimSpace(meta.BaseURL), "/")
		if needsRebuild {
			oc.clientsMu.Unlock()
			client, err := newAIClient(login, oc, key)
			if err != nil {
				// Keep the existing client if it's already in-memory; allow the login to stay cached/deletable.
				oc.clientsMu.Lock()
				existing.UserLogin = login
				login.Client = existing
				oc.clientsMu.Unlock()
				return nil
			}
			oc.clientsMu.Lock()
			oc.clients[login.ID] = client
			oc.clientsMu.Unlock()
			login.Client = client
			client.scheduleBootstrap()
			return nil
		}
		// Keep using one client instance per login ID when provider settings have not changed.
		existing.UserLogin = login
		login.Client = existing
		oc.clientsMu.Unlock()
		existing.scheduleBootstrap()
		return nil
	}
	oc.clientsMu.Unlock()

	client, err := newAIClient(login, oc, key)
	if err != nil {
		login.Client = newBrokenLoginClient(login, "Couldn't initialize this login. Remove and re-add the account.")
		return nil
	}
	oc.clientsMu.Lock()
	oc.clients[login.ID] = client
	oc.clientsMu.Unlock()
	login.Client = client
	client.scheduleBootstrap()
	return nil
}
