package providerkit

import (
	"slices"
	"strings"

	"github.com/beeper/agentremote/pkg/shared/stringutil"
)

// ApplyDefaults fills empty provider selection fields with the package defaults.
func ApplyDefaults(provider *string, fallbacks *[]string, defaultProvider string, defaultFallbacks []string) {
	if provider != nil && strings.TrimSpace(*provider) == "" {
		*provider = defaultProvider
	}
	if fallbacks != nil && len(*fallbacks) == 0 {
		*fallbacks = slices.Clone(defaultFallbacks)
	}
}

// ApplyNamedEnv fills empty provider selection fields from the provided env values.
func ApplyNamedEnv(provider *string, fallbacks *[]string, envProvider, envFallbacks string) {
	if provider != nil {
		*provider = stringutil.EnvOr(*provider, envProvider)
	}
	if fallbacks != nil && len(*fallbacks) == 0 {
		if raw := strings.TrimSpace(envFallbacks); raw != "" {
			*fallbacks = stringutil.SplitCSV(raw)
		}
	}
}
