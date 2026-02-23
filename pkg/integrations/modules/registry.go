package modules

import (
	integrationruntime "github.com/beeper/ai-bridge/pkg/integrations/runtime"
)

// BuiltinModules returns built-in integration modules in deterministic order.
func BuiltinModules(host integrationruntime.Host) []integrationruntime.ModuleHooks {
	if host == nil {
		return nil
	}
	cfg := host.ConfigLookup()
	isEnabled := func(name string) bool {
		if cfg == nil {
			return true
		}
		return cfg.ModuleEnabled(name)
	}

	out := make([]integrationruntime.ModuleHooks, 0, len(BuiltinFactories))
	for _, factory := range BuiltinFactories {
		if factory == nil {
			continue
		}
		module := factory(host)
		if module == nil {
			continue
		}
		if !isEnabled(module.Name()) {
			continue
		}
		out = append(out, module)
	}
	return out
}
