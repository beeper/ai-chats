package cron

import (
	"context"

	croncore "github.com/beeper/ai-bridge/pkg/cron"
	iruntime "github.com/beeper/ai-bridge/pkg/integrations/runtime"
)

type Host interface {
	ToolDefinitions(ctx context.Context, scope iruntime.ToolScope) []iruntime.ToolDefinition
	ExecuteTool(ctx context.Context, call iruntime.ToolCall) (handled bool, result string, err error)
	ToolAvailability(ctx context.Context, scope iruntime.ToolScope, toolName string) (known bool, available bool, source iruntime.SettingSource, reason string)

	Start(ctx context.Context) error
	Stop()
	Status() (bool, string, int, *int64, error)
	List(includeDisabled bool) ([]croncore.CronJob, error)
	Add(input croncore.CronJobCreate) (croncore.CronJob, error)
	Update(id string, patch croncore.CronJobPatch) (croncore.CronJob, error)
	Remove(id string) (bool, error)
	Run(id string, mode string) (bool, string, error)
	Wake(mode string, text string) (bool, error)
	Runs(jobID string, limit int) ([]croncore.CronRunLogEntry, error)
}

type Integration struct {
	host Host
}

func NewIntegration(host Host) *Integration {
	return &Integration{host: host}
}

func (i *Integration) Name() string { return "cron" }

func (i *Integration) ToolDefinitions(ctx context.Context, scope iruntime.ToolScope) []iruntime.ToolDefinition {
	if i == nil || i.host == nil {
		return nil
	}
	return i.host.ToolDefinitions(ctx, scope)
}

func (i *Integration) ExecuteTool(ctx context.Context, call iruntime.ToolCall) (bool, string, error) {
	if i == nil || i.host == nil {
		return false, "", nil
	}
	return i.host.ExecuteTool(ctx, call)
}

func (i *Integration) ToolAvailability(ctx context.Context, scope iruntime.ToolScope, toolName string) (bool, bool, iruntime.SettingSource, string) {
	if i == nil || i.host == nil {
		return false, false, iruntime.SourceGlobalDefault, ""
	}
	return i.host.ToolAvailability(ctx, scope, toolName)
}

func (i *Integration) Start(ctx context.Context) error {
	if i == nil || i.host == nil {
		return nil
	}
	return i.host.Start(ctx)
}

func (i *Integration) Stop() {
	if i == nil || i.host == nil {
		return
	}
	i.host.Stop()
}

func (i *Integration) Status() (bool, string, int, *int64, error) {
	if i == nil || i.host == nil {
		return false, "", 0, nil, nil
	}
	return i.host.Status()
}

func (i *Integration) List(includeDisabled bool) ([]croncore.CronJob, error) {
	if i == nil || i.host == nil {
		return nil, nil
	}
	return i.host.List(includeDisabled)
}

func (i *Integration) Add(input croncore.CronJobCreate) (croncore.CronJob, error) {
	if i == nil || i.host == nil {
		return croncore.CronJob{}, nil
	}
	return i.host.Add(input)
}

func (i *Integration) Update(id string, patch croncore.CronJobPatch) (croncore.CronJob, error) {
	if i == nil || i.host == nil {
		return croncore.CronJob{}, nil
	}
	return i.host.Update(id, patch)
}

func (i *Integration) Remove(id string) (bool, error) {
	if i == nil || i.host == nil {
		return false, nil
	}
	return i.host.Remove(id)
}

func (i *Integration) Run(id string, mode string) (bool, string, error) {
	if i == nil || i.host == nil {
		return false, "", nil
	}
	return i.host.Run(id, mode)
}

func (i *Integration) Wake(mode string, text string) (bool, error) {
	if i == nil || i.host == nil {
		return false, nil
	}
	return i.host.Wake(mode, text)
}

func (i *Integration) Runs(jobID string, limit int) ([]croncore.CronRunLogEntry, error) {
	if i == nil || i.host == nil {
		return nil, nil
	}
	return i.host.Runs(jobID, limit)
}
