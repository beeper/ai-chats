package cron

import (
	"strings"

	"github.com/beeper/ai-bridge/pkg/shared/maputil"
)

func (i *Integration) ToolApprovalRequirement(toolName string, args map[string]any) (handled bool, required bool, action string) {
	if i == nil {
		return false, false, ""
	}
	if !strings.EqualFold(strings.TrimSpace(toolName), "cron") {
		return false, false, ""
	}
	action = strings.ToLower(strings.TrimSpace(maputil.StringArg(args, "action")))
	switch action {
	case "status", "list", "runs":
		return true, false, action
	default:
		if action == "" {
			action = "action"
		}
		return true, true, action
	}
}
