package modules

import integrationruntime "github.com/beeper/agentremote/pkg/integrations/runtime"

func BuiltinModules(host integrationruntime.Host) []integrationruntime.ModuleHooks {
	if host == nil {
		return nil
	}
	out := make([]integrationruntime.ModuleHooks, 0, len(BuiltinFactories))
	for _, factory := range BuiltinFactories {
		module := factory(host)
		if module == nil {
			continue
		}
		if !host.ModuleEnabled(module.Name()) {
			continue
		}
		out = append(out, module)
	}
	return out
}
