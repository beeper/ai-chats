package providerresource

import (
	"errors"

	"github.com/beeper/agentremote/pkg/shared/providerchain"
	"github.com/beeper/agentremote/pkg/shared/registry"
	"github.com/beeper/agentremote/pkg/shared/stringutil"
)

// Run executes a provider chain after registering available providers.
func Run[P registry.Named, R any](
	provider string,
	fallbacks []string,
	defaultFallbackOrder []string,
	register func(*registry.Registry[P]),
	exec func(P) (*R, error),
	decorate func(string, *R),
	noProviderErr error,
) (*R, error) {
	reg := registry.New[P]()
	register(reg)
	order := stringutil.BuildProviderOrder(provider, fallbacks, defaultFallbackOrder)
	if noProviderErr == nil {
		noProviderErr = errors.New("no providers available")
	}
	return providerchain.RunFirst(order, reg.Get, exec, decorate, noProviderErr)
}
