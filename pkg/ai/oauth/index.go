package oauth

import (
	"fmt"
	"sync"
	"time"
)

type providerInfo struct {
	provider Provider
	builtin  bool
}

var (
	providersMu sync.RWMutex
	providers   = map[ProviderID]providerInfo{}
)

func GetProvider(id ProviderID) (Provider, bool) {
	providersMu.RLock()
	defer providersMu.RUnlock()
	entry, ok := providers[id]
	return entry.provider, ok
}

func RegisterProvider(provider Provider) {
	providersMu.Lock()
	defer providersMu.Unlock()
	providers[provider.ID()] = providerInfo{provider: provider}
}

func RegisterBuiltinProvider(provider Provider) {
	providersMu.Lock()
	defer providersMu.Unlock()
	providers[provider.ID()] = providerInfo{provider: provider, builtin: true}
}

func UnregisterProvider(id ProviderID) {
	providersMu.Lock()
	defer providersMu.Unlock()
	if entry, ok := providers[id]; ok && entry.builtin {
		return
	}
	delete(providers, id)
}

func ResetProviders() {
	providersMu.Lock()
	defer providersMu.Unlock()
	next := map[ProviderID]providerInfo{}
	for id, entry := range providers {
		if entry.builtin {
			next[id] = entry
		}
	}
	providers = next
}

func GetProviders() []Provider {
	providersMu.RLock()
	defer providersMu.RUnlock()
	out := make([]Provider, 0, len(providers))
	for _, entry := range providers {
		out = append(out, entry.provider)
	}
	return out
}

func RefreshToken(providerID ProviderID, credentials Credentials) (Credentials, error) {
	provider, ok := GetProvider(providerID)
	if !ok {
		return Credentials{}, fmt.Errorf("unknown OAuth provider: %s", providerID)
	}
	return provider.RefreshToken(credentials)
}

func GetAPIKey(providerID ProviderID, credentials map[ProviderID]Credentials) (*Credentials, string, error) {
	provider, ok := GetProvider(providerID)
	if !ok {
		return nil, "", fmt.Errorf("unknown OAuth provider: %s", providerID)
	}
	creds, ok := credentials[providerID]
	if !ok {
		return nil, "", nil
	}
	if time.Now().UnixMilli() >= creds.Expires {
		refreshed, err := provider.RefreshToken(creds)
		if err != nil {
			return nil, "", fmt.Errorf("failed to refresh OAuth token for %s", providerID)
		}
		creds = refreshed
	}
	key := provider.GetAPIKey(creds)
	return &creds, key, nil
}
