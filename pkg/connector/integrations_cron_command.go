package connector

import (
	"strconv"
	"strings"

	"maunium.net/go/mautrix/bridgev2/commands"

	"github.com/beeper/ai-bridge/pkg/connector/commandregistry"
)

// CommandCron handles the !ai cron command.
var CommandCron = registerAICommand(commandregistry.Definition{
	Name:           "cron",
	Description:    "Inspect/manage cron jobs",
	Args:           "[status|list|runs|run|remove] ...",
	Section:        HelpSectionAI,
	RequiresPortal: true,
	RequiresLogin:  true,
	Handler:        fnCron,
})

func fnCron(ce *commands.Event) {
	client, _, ok := requireClientMeta(ce)
	if !ok {
		return
	}
	cronModule := client.cronModule()
	if cronModule == nil {
		ce.Reply("Cron service not available.")
		return
	}

	action := "status"
	if len(ce.Args) > 0 {
		action = strings.ToLower(strings.TrimSpace(ce.Args[0]))
	}
	switch action {
	case "status":
		enabled, storePath, jobCount, nextWake, err := cronModule.Status()
		if err != nil {
			ce.Reply("Cron status failed: %s", err.Error())
			return
		}
		ce.Reply(formatCronStatusText(enabled, storePath, jobCount, nextWake))
	case "list":
		includeDisabled := false
		if len(ce.Args) > 1 && (strings.EqualFold(ce.Args[1], "all") || strings.EqualFold(ce.Args[1], "--all")) {
			includeDisabled = true
		}
		jobs, err := cronModule.List(includeDisabled)
		if err != nil {
			ce.Reply("Cron list failed: %s", err.Error())
			return
		}
		ce.Reply(formatCronJobListText(jobs))
	case "runs":
		if len(ce.Args) < 2 || strings.TrimSpace(ce.Args[1]) == "" {
			ce.Reply("Usage: `!ai cron runs <jobId> [limit]`")
			return
		}
		jobID := strings.TrimSpace(ce.Args[1])
		limit := 50
		if len(ce.Args) > 2 && strings.TrimSpace(ce.Args[2]) != "" {
			if n, err := strconv.Atoi(strings.TrimSpace(ce.Args[2])); err == nil && n > 0 {
				limit = n
			}
		}
		entries, err := cronModule.Runs(jobID, limit)
		if err != nil {
			ce.Reply("Cron runs failed: %s", err.Error())
			return
		}
		ce.Reply(formatCronRunsText(jobID, entries))
	case "remove", "rm", "delete":
		if len(ce.Args) < 2 || strings.TrimSpace(ce.Args[1]) == "" {
			ce.Reply("Usage: `!ai cron remove <jobId>`")
			return
		}
		jobID := strings.TrimSpace(ce.Args[1])
		removed, err := cronModule.Remove(jobID)
		if err != nil {
			ce.Reply("Cron remove failed: %s", err.Error())
			return
		}
		if removed {
			ce.Reply("Removed.")
		} else {
			ce.Reply("No such job (already removed?).")
		}
	case "run":
		if len(ce.Args) < 2 || strings.TrimSpace(ce.Args[1]) == "" {
			ce.Reply("Usage: `!ai cron run <jobId> [force]`")
			return
		}
		jobID := strings.TrimSpace(ce.Args[1])
		mode := ""
		if len(ce.Args) > 2 && strings.EqualFold(strings.TrimSpace(ce.Args[2]), "force") {
			mode = "force"
		}
		ran, reason, err := cronModule.Run(jobID, mode)
		if err != nil {
			ce.Reply("Cron run failed: %s", err.Error())
			return
		}
		if ran {
			ce.Reply("Triggered.")
			return
		}
		if strings.TrimSpace(reason) == "" {
			reason = "not-due"
		}
		ce.Reply("Not run (%s).", reason)
	default:
		ce.Reply("Usage:\n- `!ai cron status`\n- `!ai cron list [all]`\n- `!ai cron runs <jobId> [limit]`\n- `!ai cron run <jobId> [force]`\n- `!ai cron remove <jobId>`")
	}
}
