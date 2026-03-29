package ai

import (
	"strings"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"

	"github.com/beeper/agentremote/pkg/shared/stringutil"
)

const (
	noAPIKeyLoginError   = "No API key available for this login. Sign in again or remove this account."
	initLoginClientError = "Couldn't initialize this login. Remove and re-add the account."
)

func reuseAIClient(login *bridgev2.UserLogin, client *AIClient, bootstrap bool) {
	if login == nil || client == nil {
		return
	}
	client.SetUserLogin(login)
	login.Client = client
	if bootstrap {
		client.scheduleBootstrap()
	}
}

func aiClientNeedsRebuild(existing *AIClient, key string, meta *UserLoginMetadata) bool {
	if existing == nil {
		return true
	}
	existingMeta := loginMetadata(existing.UserLogin)
	existingProvider := ""
	existingBaseURL := ""
	if existingMeta != nil {
		existingProvider = strings.TrimSpace(existingMeta.Provider)
		existingBaseURL = stringutil.NormalizeBaseURL(loginCredentialBaseURL(existingMeta))
	}
	targetProvider := ""
	targetBaseURL := ""
	if meta != nil {
		targetProvider = strings.TrimSpace(meta.Provider)
		targetBaseURL = stringutil.NormalizeBaseURL(loginCredentialBaseURL(meta))
	}
	return existing.apiKey != key ||
		!strings.EqualFold(existingProvider, targetProvider) ||
		existingBaseURL != targetBaseURL
}

func (oc *OpenAIConnector) lookupCachedAIClient(loginID networkid.UserLoginID) (bridgev2.NetworkAPI, *AIClient) {
	oc.clientsMu.Lock()
	defer oc.clientsMu.Unlock()
	cachedAPI := oc.clients[loginID]
	cached, _ := cachedAPI.(*AIClient)
	return cachedAPI, cached
}

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

func (oc *OpenAIConnector) publishOrReuseClient(login *bridgev2.UserLogin, created *AIClient, replace *AIClient) *AIClient {
	if login == nil || created == nil {
		return nil
	}
	oc.clientsMu.Lock()
	if cached, ok := oc.clients[login.ID].(*AIClient); ok && cached != nil && cached != replace {
		reuseAIClient(login, cached, false)
		oc.clientsMu.Unlock()
		created.Disconnect()
		return cached
	}
	var disconnectReplace *AIClient
	if replace != nil && replace != created {
		disconnectReplace = replace
	}
	oc.clients[login.ID] = created
	reuseAIClient(login, created, false)
	oc.clientsMu.Unlock()
	if disconnectReplace != nil {
		disconnectReplace.Disconnect()
	}
	return created
}

func (oc *OpenAIConnector) loadAIUserLogin(login *bridgev2.UserLogin, meta *UserLoginMetadata) error {
	if login == nil {
		return nil
	}
	key := strings.TrimSpace(oc.resolveProviderAPIKey(meta))
	cachedAPI, existing := oc.lookupCachedAIClient(login.ID)
	if key == "" {
		oc.evictCachedClient(login.ID, nil)
		login.Client = newBrokenLoginClient(login, noAPIKeyLoginError)
		return nil
	}

	if existing != nil && !aiClientNeedsRebuild(existing, key, meta) {
		reuseAIClient(login, existing, true)
		return nil
	}

	if cachedAPI != nil && existing == nil {
		oc.evictCachedClient(login.ID, cachedAPI)
	}

	client, err := newAIClient(login, oc, key)
	if err != nil {
		// Keep the existing client if rebuilding failed.
		if existing != nil {
			reuseAIClient(login, existing, false)
			return nil
		}
		login.Client = newBrokenLoginClient(login, initLoginClientError)
		return nil
	}

	chosen := oc.publishOrReuseClient(login, client, existing)
	if chosen != nil {
		chosen.scheduleBootstrap()
	}
	return nil
}
