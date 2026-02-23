package modules

import (
	integrationcron "github.com/beeper/ai-bridge/pkg/integrations/cron"
	integrationmemory "github.com/beeper/ai-bridge/pkg/integrations/memory"
	integrationruntime "github.com/beeper/ai-bridge/pkg/integrations/runtime"
)

// BuiltinFactories is the compile-time module selection list.
// Removing one import line and one factory line cleanly excludes a module.
var BuiltinFactories = []integrationruntime.ModuleFactory{
	integrationcron.New,
	integrationmemory.New,
}
