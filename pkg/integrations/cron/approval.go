package cron

import (
	"strings"
)

func (i *Integration) ToolApprovalRequirement(toolName string, args map[string]any) (handled bool, required bool, action string) {
	if i == nil {
		return false, false, ""
	}
	if !strings.EqualFold(strings.TrimSpace(toolName), "cron") {
		return false, false, ""
	}
	action = strings.ToLower(strings.TrimSpace(readStringArg(args, "action")))
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

func readStringArg(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	if raw, ok := args[key]; ok {
		if s, ok := raw.(string); ok {
			return s
		}
	}
	return ""
}
