package cron

import (
	"context"
	"strconv"
	"strings"

	iruntime "github.com/beeper/ai-bridge/pkg/integrations/runtime"
)

type Host interface {
	ToolDefinitions(ctx context.Context, scope iruntime.ToolScope) []iruntime.ToolDefinition
	ExecuteTool(ctx context.Context, call iruntime.ToolCall) (handled bool, result string, err error)
	ToolAvailability(ctx context.Context, scope iruntime.ToolScope, toolName string) (known bool, available bool, source iruntime.SettingSource, reason string)

	Start(ctx context.Context) error
	Stop()
	Status() (bool, string, int, *int64, error)
	List(includeDisabled bool) ([]Job, error)
	Add(input JobCreate) (Job, error)
	Update(id string, patch JobPatch) (Job, error)
	Remove(id string) (bool, error)
	Run(id string, mode string) (bool, string, error)
	Wake(mode string, text string) (bool, error)
	Runs(jobID string, limit int) ([]RunLogEntry, error)
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
	defs := i.host.ToolDefinitions(ctx, scope)
	out := make([]iruntime.ToolDefinition, 0, 1)
	for _, def := range defs {
		if strings.EqualFold(strings.TrimSpace(def.Name), "cron") {
			out = append(out, def)
			break
		}
	}
	return out
}

func (i *Integration) ExecuteTool(ctx context.Context, call iruntime.ToolCall) (bool, string, error) {
	if i == nil || i.host == nil {
		return false, "", nil
	}
	if !strings.EqualFold(strings.TrimSpace(call.Name), "cron") {
		return false, "", nil
	}
	return i.host.ExecuteTool(ctx, call)
}

func (i *Integration) ToolAvailability(ctx context.Context, scope iruntime.ToolScope, toolName string) (bool, bool, iruntime.SettingSource, string) {
	if i == nil || i.host == nil {
		return false, false, iruntime.SourceGlobalDefault, ""
	}
	if !strings.EqualFold(strings.TrimSpace(toolName), "cron") {
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

func (i *Integration) List(includeDisabled bool) ([]Job, error) {
	if i == nil || i.host == nil {
		return nil, nil
	}
	return i.host.List(includeDisabled)
}

func (i *Integration) Add(input JobCreate) (Job, error) {
	if i == nil || i.host == nil {
		return Job{}, nil
	}
	return i.host.Add(input)
}

func (i *Integration) Update(id string, patch JobPatch) (Job, error) {
	if i == nil || i.host == nil {
		return Job{}, nil
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

func (i *Integration) Runs(jobID string, limit int) ([]RunLogEntry, error) {
	if i == nil || i.host == nil {
		return nil, nil
	}
	return i.host.Runs(jobID, limit)
}

func (i *Integration) CommandDefinitions(_ context.Context, _ iruntime.CommandScope) []iruntime.CommandDefinition {
	return []iruntime.CommandDefinition{{
		Name:           "cron",
		Description:    "Inspect/manage cron jobs",
		Args:           "[status|list|runs|run|remove] ...",
		RequiresPortal: true,
		RequiresLogin:  true,
	}}
}

func (i *Integration) ExecuteCommand(_ context.Context, call iruntime.CommandCall) (bool, error) {
	if i == nil || i.host == nil {
		return false, nil
	}
	if strings.ToLower(strings.TrimSpace(call.Name)) != "cron" {
		return false, nil
	}
	reply := call.Reply
	if reply == nil {
		reply = func(string, ...any) {}
	}
	action := "status"
	if len(call.Args) > 0 {
		action = strings.ToLower(strings.TrimSpace(call.Args[0]))
	}
	switch action {
	case "status":
		enabled, storePath, jobCount, nextWake, err := i.Status()
		if err != nil {
			reply("Cron status failed: %s", err.Error())
			return true, nil
		}
		reply(FormatCronStatusText(enabled, storePath, jobCount, nextWake))
	case "list":
		includeDisabled := false
		if len(call.Args) > 1 && (strings.EqualFold(call.Args[1], "all") || strings.EqualFold(call.Args[1], "--all")) {
			includeDisabled = true
		}
		jobs, err := i.List(includeDisabled)
		if err != nil {
			reply("Cron list failed: %s", err.Error())
			return true, nil
		}
		reply(FormatCronJobListText(jobs))
	case "runs":
		if len(call.Args) < 2 || strings.TrimSpace(call.Args[1]) == "" {
			reply("Usage: `!ai cron runs <jobId> [limit]`")
			return true, nil
		}
		jobID := strings.TrimSpace(call.Args[1])
		limit := 50
		if len(call.Args) > 2 && strings.TrimSpace(call.Args[2]) != "" {
			if n, err := strconv.Atoi(strings.TrimSpace(call.Args[2])); err == nil && n > 0 {
				limit = n
			}
		}
		entries, err := i.Runs(jobID, limit)
		if err != nil {
			reply("Cron runs failed: %s", err.Error())
			return true, nil
		}
		reply(FormatCronRunsText(jobID, entries))
	case "remove", "rm", "delete":
		if len(call.Args) < 2 || strings.TrimSpace(call.Args[1]) == "" {
			reply("Usage: `!ai cron remove <jobId>`")
			return true, nil
		}
		jobID := strings.TrimSpace(call.Args[1])
		removed, err := i.Remove(jobID)
		if err != nil {
			reply("Cron remove failed: %s", err.Error())
			return true, nil
		}
		if removed {
			reply("Removed.")
		} else {
			reply("No such job (already removed?).")
		}
	case "run":
		if len(call.Args) < 2 || strings.TrimSpace(call.Args[1]) == "" {
			reply("Usage: `!ai cron run <jobId> [force]`")
			return true, nil
		}
		jobID := strings.TrimSpace(call.Args[1])
		mode := ""
		if len(call.Args) > 2 && strings.EqualFold(strings.TrimSpace(call.Args[2]), "force") {
			mode = "force"
		}
		ran, reason, err := i.Run(jobID, mode)
		if err != nil {
			reply("Cron run failed: %s", err.Error())
			return true, nil
		}
		if ran {
			reply("Triggered.")
			return true, nil
		}
		if strings.TrimSpace(reason) == "" {
			reason = "not-due"
		}
		reply("Not run (%s).", reason)
	default:
		reply("Usage:\n- `!ai cron status`\n- `!ai cron list [all]`\n- `!ai cron runs <jobId> [limit]`\n- `!ai cron run <jobId> [force]`\n- `!ai cron remove <jobId>`")
	}
	return true, nil
}
