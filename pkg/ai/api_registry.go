package ai

import (
	"fmt"
	"sync"
)

type StreamFn func(model Model, context Context, options *StreamOptions) *AssistantMessageEventStream
type StreamSimpleFn func(model Model, context Context, options *SimpleStreamOptions) *AssistantMessageEventStream

type APIProvider struct {
	API          Api
	Stream       StreamFn
	StreamSimple StreamSimpleFn
}

type registeredProvider struct {
	provider APIProvider
	sourceID string
}

var (
	registryMu sync.RWMutex
	registry   = map[Api]registeredProvider{}
)

func RegisterAPIProvider(provider APIProvider, sourceID string) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[provider.API] = registeredProvider{
		provider: provider,
		sourceID: sourceID,
	}
}

func GetAPIProvider(api Api) (APIProvider, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	entry, ok := registry[api]
	return entry.provider, ok
}

func GetAPIProviders() []APIProvider {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]APIProvider, 0, len(registry))
	for _, entry := range registry {
		out = append(out, entry.provider)
	}
	return out
}

func UnregisterAPIProviders(sourceID string) {
	registryMu.Lock()
	defer registryMu.Unlock()
	for api, entry := range registry {
		if entry.sourceID == sourceID {
			delete(registry, api)
		}
	}
}

func ClearAPIProviders() {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry = map[Api]registeredProvider{}
}

func ResolveAPIProvider(api Api) (APIProvider, error) {
	provider, ok := GetAPIProvider(api)
	if !ok {
		return APIProvider{}, fmt.Errorf("no API provider registered for api: %s", api)
	}
	return provider, nil
}
