package providers

import "github.com/beeper/ai-bridge/pkg/ai"

const BuiltinProviderSourceID = "pkg/ai/providers/register_builtins"

// RegisterBuiltInAPIProviders registers providers implemented in this package.
// Initial scaffold keeps registry empty until concrete provider streamers are ported.
func RegisterBuiltInAPIProviders() {}

func ResetAPIProviders() {
	ai.ClearAPIProviders()
	RegisterBuiltInAPIProviders()
}
