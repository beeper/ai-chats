package cron

import (
	"context"
	"encoding/json"
	"strings"

	iruntime "github.com/beeper/agentremote/pkg/integrations/runtime"
	"github.com/beeper/agentremote/pkg/shared/toolspec"
)

const moduleName = "cron"

type Scheduler interface {
	CronStatus(ctx context.Context) (enabled bool, backend string, jobCount int, nextRun *int64, err error)
	CronList(ctx context.Context, includeDisabled bool) ([]Job, error)
	CronAdd(ctx context.Context, input JobCreate) (Job, error)
	CronUpdate(ctx context.Context, jobID string, patch JobPatch) (Job, error)
	CronRemove(ctx context.Context, jobID string) (bool, error)
	CronRun(ctx context.Context, jobID string) (bool, string, error)
}

type Integration struct {
	host      iruntime.Host
	scheduler Scheduler
}

func New(host iruntime.Host) iruntime.ModuleHooks {
	return NewWithScheduler(host, nil)
}

func NewWithScheduler(host iruntime.Host, scheduler Scheduler) iruntime.ModuleHooks {
	return iruntime.ModuleOrNil(host, func(host iruntime.Host) *Integration {
		return &Integration{host: host, scheduler: scheduler}
	})
}

func (i *Integration) Name() string { return moduleName }

func (i *Integration) Start(context.Context) error { return nil }

func (i *Integration) Stop() {}

func (i *Integration) ToolDefinitions(_ context.Context, _ iruntime.ToolScope) []iruntime.ToolDefinition {
	return []iruntime.ToolDefinition{{
		Name:        toolspec.CronName,
		Description: toolspec.CronDescription,
		Parameters:  toolspec.CronSchema(),
	}}
}

func (i *Integration) ExecuteTool(ctx context.Context, call iruntime.ToolCall) (bool, string, error) {
	if !iruntime.MatchesName(call.Name, toolspec.CronName) {
		return false, "", nil
	}
	result, err := ExecuteTool(ctx, call.Args, i.buildToolExecDeps(ctx, call.Scope))
	return true, result, err
}

func (i *Integration) ToolAvailability(_ context.Context, _ iruntime.ToolScope, toolName string) (bool, bool, iruntime.SettingSource, string) {
	if !iruntime.MatchesName(toolName, toolspec.CronName) {
		return false, false, iruntime.SourceGlobalDefault, ""
	}
	if i.scheduler == nil {
		return true, false, iruntime.SourceProviderLimit, "Scheduler not available"
	}
	return true, true, iruntime.SourceGlobalDefault, ""
}

func (i *Integration) CommandDefinitions(_ context.Context, _ iruntime.CommandScope) []iruntime.CommandDefinition {
	return []iruntime.CommandDefinition{{
		Name:           "cron",
		Description:    "Inspect/manage scheduled jobs",
		Args:           "[status|list|add|update|run|remove] ...",
		RequiresPortal: true,
		RequiresLogin:  true,
	}}
}

func (i *Integration) ExecuteCommand(ctx context.Context, call iruntime.CommandCall) (bool, error) {
	if !iruntime.MatchesName(call.Name, moduleName) {
		return false, nil
	}
	return true, i.executeCronCommand(ctx, call)
}

func (i *Integration) executeCronCommand(ctx context.Context, call iruntime.CommandCall) error {
	reply := call.Reply
	if reply == nil {
		reply = func(string, ...any) {}
	}
	if i.scheduler == nil {
		reply("Scheduler not available.")
		return nil
	}
	action := "status"
	if len(call.Args) > 0 {
		action = strings.ToLower(strings.TrimSpace(call.Args[0]))
	}
	switch action {
	case "status":
		enabled, backend, jobCount, nextRun, err := i.scheduler.CronStatus(ctx)
		if err != nil {
			reply("Cron status failed: %s", err.Error())
			return nil
		}
		reply("%s", formatCronStatusText(enabled, backend, jobCount, nextRun))
	case "list":
		includeDisabled := false
		if len(call.Args) > 1 && (strings.EqualFold(call.Args[1], "all") || strings.EqualFold(call.Args[1], "--all")) {
			includeDisabled = true
		}
		jobs, err := i.scheduler.CronList(ctx, includeDisabled)
		if err != nil {
			reply("Cron list failed: %s", err.Error())
			return nil
		}
		reply("%s", formatCronJobListText(jobs))
	case "add":
		rawJSON := strings.TrimSpace(strings.TrimPrefix(call.RawArgs, action))
		if rawJSON == "" {
			rawJSON = strings.TrimSpace(strings.Join(call.Args[1:], " "))
		}
		if rawJSON == "" {
			reply("Usage: `!ai cron add <job-json>`")
			return nil
		}
		var raw map[string]any
		if err := json.Unmarshal([]byte(rawJSON), &raw); err != nil {
			reply("Cron add failed: invalid JSON (%s)", err.Error())
			return nil
		}
		input, err := NormalizeJobCreateRaw(raw)
		if err != nil {
			reply("Cron add failed: %s", err.Error())
			return nil
		}
		deps := i.buildToolExecDeps(ctx, iruntime.ToolScope(call.Scope))
		injectToolContext(&input, deps.ResolveCreateContext)
		if input.Delivery != nil && strings.EqualFold(strings.TrimSpace(string(input.Delivery.Mode)), "announce") && deps.ValidateDeliveryTo != nil {
			if err := deps.ValidateDeliveryTo(input.Delivery.To); err != nil {
				reply("Cron add failed: %s", err.Error())
				return nil
			}
		}
		job, err := i.scheduler.CronAdd(ctx, input)
		if err != nil {
			reply("Cron add failed: %s", err.Error())
			return nil
		}
		reply("Added `%s`.", job.ID)
	case "update":
		if len(call.Args) < 3 {
			reply("Usage: `!ai cron update <jobId> <patch-json>`")
			return nil
		}
		jobID := strings.TrimSpace(call.Args[1])
		rawJSON := strings.TrimSpace(strings.Join(call.Args[2:], " "))
		var raw map[string]any
		if err := json.Unmarshal([]byte(rawJSON), &raw); err != nil {
			reply("Cron update failed: invalid JSON (%s)", err.Error())
			return nil
		}
		patch, err := NormalizeJobPatchRaw(raw)
		if err != nil {
			reply("Cron update failed: %s", err.Error())
			return nil
		}
		deps := i.buildToolExecDeps(ctx, iruntime.ToolScope(call.Scope))
		if patch.Delivery != nil && patch.Delivery.To != nil && deps.ValidateDeliveryTo != nil {
			if err := deps.ValidateDeliveryTo(*patch.Delivery.To); err != nil {
				reply("Cron update failed: %s", err.Error())
				return nil
			}
		}
		job, err := i.scheduler.CronUpdate(ctx, jobID, patch)
		if err != nil {
			reply("Cron update failed: %s", err.Error())
			return nil
		}
		reply("Updated `%s`.", job.ID)
	case "remove", "rm", "delete":
		if len(call.Args) < 2 || strings.TrimSpace(call.Args[1]) == "" {
			reply("Usage: `!ai cron remove <jobId>`")
			return nil
		}
		removed, err := i.scheduler.CronRemove(ctx, strings.TrimSpace(call.Args[1]))
		if err != nil {
			reply("Cron remove failed: %s", err.Error())
			return nil
		}
		if removed {
			reply("Removed.")
		} else {
			reply("No such job.")
		}
	case "run":
		if len(call.Args) < 2 || strings.TrimSpace(call.Args[1]) == "" {
			reply("Usage: `!ai cron run <jobId>`")
			return nil
		}
		ran, reason, err := i.scheduler.CronRun(ctx, strings.TrimSpace(call.Args[1]))
		if err != nil {
			reply("Cron run failed: %s", err.Error())
			return nil
		}
		if ran {
			reply("Triggered.")
			return nil
		}
		if strings.TrimSpace(reason) == "" {
			reason = "not-run"
		}
		reply("Not run (%s).", reason)
	default:
		reply("Usage:\n- `!ai cron status`\n- `!ai cron list [all]`\n- `!ai cron add <job-json>`\n- `!ai cron update <jobId> <patch-json>`\n- `!ai cron run <jobId>`\n- `!ai cron remove <jobId>`")
	}
	return nil
}

func (i *Integration) buildToolExecDeps(ctx context.Context, scope iruntime.ToolScope) ToolExecDeps {
	deps := ToolExecDeps{
		NowMs: func() int64 { return i.host.Now().UnixMilli() },
		ResolveCreateContext: func() ToolCreateContext {
			agentID := i.host.DefaultAgentID()
			if scope.Meta != nil {
				if resolved := strings.TrimSpace(scope.Meta.AgentID()); resolved != "" {
					agentID = resolved
				}
			}
			roomID := ""
			if scope.Portal != nil {
				roomID = scope.Portal.MXID.String()
			}
			sourceInternal := false
			if scope.Meta != nil {
				sourceInternal = scope.Meta.InternalRoom()
			}
			return ToolCreateContext{AgentID: agentID, SourceInternal: sourceInternal, SourceRoomID: roomID}
		},
		ResolveReminderLines: func(count int) []ReminderContextLine {
			if scope.Portal == nil {
				return nil
			}
			msgs := i.host.RecentMessages(ctx, scope.Portal, count)
			lines := make([]ReminderContextLine, 0, len(msgs))
			for _, msg := range msgs {
				lines = append(lines, ReminderContextLine{Role: msg.Role, Text: msg.Body})
			}
			return lines
		},
		ValidateDeliveryTo: ValidateDeliveryTo,
	}
	if i.scheduler == nil {
		return deps
	}
	deps.Status = func() (bool, string, int, *int64, error) {
		return i.scheduler.CronStatus(ctx)
	}
	deps.List = func(includeDisabled bool) ([]Job, error) {
		return i.scheduler.CronList(ctx, includeDisabled)
	}
	deps.Add = func(input JobCreate) (Job, error) {
		return i.scheduler.CronAdd(ctx, input)
	}
	deps.Update = func(jobID string, patch JobPatch) (Job, error) {
		return i.scheduler.CronUpdate(ctx, jobID, patch)
	}
	deps.Remove = func(jobID string) (bool, error) {
		return i.scheduler.CronRemove(ctx, jobID)
	}
	deps.Run = func(jobID string) (bool, string, error) {
		return i.scheduler.CronRun(ctx, jobID)
	}
	return deps
}

var (
	_ iruntime.ToolIntegration      = (*Integration)(nil)
	_ iruntime.CommandIntegration   = (*Integration)(nil)
	_ iruntime.LifecycleIntegration = (*Integration)(nil)
)
