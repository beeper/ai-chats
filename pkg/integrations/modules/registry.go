package modules

import (
	integrationruntime "github.com/beeper/agentremote/pkg/integrations/runtime"
)

// BuiltinModules returns built-in integration modules in deterministic order.
func BuiltinModules(host integrationruntime.Host) []integrationruntime.ModuleHooks {
	if host == nil {
		return nil
	}
	cfg := host.ConfigLookup()
	out := make([]integrationruntime.ModuleHooks, 0, len(BuiltinFactories))
	for _, factory := range BuiltinFactories {
		if factory == nil {
			continue
		}
		module := factory(host)
		if module == nil {
			continue
		}
		if cfg != nil && !cfg.ModuleEnabled(module.Name()) {
			continue
		}
		out = append(out, module)
	}
	return out
}
