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

// ApplyEnvDefaults merges environment-derived defaults into a config after the
// config-specific defaulting has been applied.
func ApplyEnvDefaults[C any](
	cfg *C,
	configFromEnv func() *C,
	withDefaults func(*C) *C,
	hasProvider func(*C) bool,
	hasFallbacks func(*C) bool,
	merge func(current, env *C, hasProvider, hasFallbacks bool),
) *C {
	if cfg == nil {
		return configFromEnv()
	}
	current := withDefaults(cfg)
	envCfg := configFromEnv()
	merge(current, envCfg, hasProvider(cfg), hasFallbacks(cfg))
	return current
}
