package modules

import (
	integrationcron "github.com/beeper/ai-bridge/pkg/integrations/cron"
	integrationmemory "github.com/beeper/ai-bridge/pkg/integrations/memory"
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

	out := make([]integrationruntime.ModuleHooks, 0, 2)
	if isEnabled("cron") {
		if cronHost, ok := any(host).(integrationcron.Host); ok {
			out = append(out, integrationcron.NewIntegration(cronHost))
		}
	}
	if isEnabled("memory") {
		if memoryHost, ok := any(host).(integrationmemory.Host); ok {
			out = append(out, integrationmemory.NewIntegration(memoryHost))
		}
	}
	return out
}

// BuiltinCommandDefinitions returns command definitions exposed by built-in modules.
func BuiltinCommandDefinitions() []integrationruntime.CommandDefinition {
	return []integrationruntime.CommandDefinition{
		{
			Name:           "cron",
			Description:    "Inspect/manage cron jobs",
			Args:           "[status|list|runs|run|remove] ...",
			RequiresPortal: true,
			RequiresLogin:  true,
		},
		{
			Name:           "memory",
			Description:    "Inspect and edit memory files/index",
			Args:           "<status|reindex|search|get|set|append> [...]",
			RequiresPortal: true,
			RequiresLogin:  true,
			AdminOnly:      true,
		},
	}
}
