package connector

import (
	"context"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

const (
	noAPIKeyLoginError   = "No API key available for this login. Sign in again or remove this account."
	initLoginClientError = "Couldn't initialize this login. Remove and re-add the account."
)

func (oc *OpenAIConnector) evictCachedClient(loginID networkid.UserLoginID, expected bridgev2.NetworkAPI) {
	oc.clientsMu.Lock()
	cachedAPI := oc.clients[loginID]
	if expected != nil && cachedAPI != expected {
		oc.clientsMu.Unlock()
		return
	}
	delete(oc.clients, loginID)
	oc.clientsMu.Unlock()
	if cached, ok := cachedAPI.(*AIClient); ok && cached != nil {
		cached.Disconnect()
	}
}

func (oc *OpenAIConnector) loadAIUserLogin(ctx context.Context, login *bridgev2.UserLogin, meta *UserLoginMetadata, cfg *aiLoginConfig) error {
	if login == nil {
		return nil
	}
	if meta == nil {
		meta = loginMetadata(login)
	}
	if meta == nil {
		oc.evictCachedClient(login.ID, nil)
		login.Client = newBrokenLoginClient(login, initLoginClientError)
		return nil
	}
	if cfg == nil {
		var err error
		cfg, err = loadAILoginConfig(ctx, login)
		if err != nil {
			return err
		}
	}
	if cfg == nil {
		cfg = &aiLoginConfig{}
	}
	key := strings.TrimSpace(oc.resolveProviderAPIKeyForConfig(meta.Provider, cfg))
	if key == "" {
		oc.evictCachedClient(login.ID, nil)
		login.Client = newBrokenLoginClient(login, noAPIKeyLoginError)
		return nil
	}

	client, err := newAIClient(login, oc, key, cfg)
	if err != nil {
		login.Client = newBrokenLoginClient(login, initLoginClientError)
		return nil
	}

	oc.clientsMu.Lock()
	previous := oc.clients[login.ID]
	oc.clients[login.ID] = client
	login.Client = client
	oc.clientsMu.Unlock()
	if old, ok := previous.(*AIClient); ok && old != nil && old != client {
		old.Disconnect()
	}
	return nil
}
