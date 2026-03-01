package connector

import (
	"strings"

	"github.com/beeper/ai-bridge/pkg/shared/maputil"
)

func (oc *AIClient) builtinToolApprovalRequirement(toolName string, args map[string]any) (required bool, action string) {
	if oc == nil || !oc.toolApprovalsRuntimeEnabled() {
		return false, ""
	}
	toolName = strings.TrimSpace(toolName)
	if toolName == "" || !oc.toolApprovalsRequireForTool(toolName) {
		return false, ""
	}
	switch normalizeApprovalToken(toolName) {
	case normalizeApprovalToken(ToolNameMessage):
		action = normalizeMessageAction(maputil.StringArg(args, "action"))
		switch action {
		// Read-only / non-destructive actions (do not require approval).
		case "reactions", "search", "read", "member-info", "channel-info", "list-pins",
			// Desktop API read-only surface (ai-bridge message tool actions).
			"desktop-list-chats", "desktop-search-chats", "desktop-search-messages", "desktop-download-asset":
			return false, action
		default:
			return true, action
		}
	default:
		if handled, required, action := oc.integratedToolApprovalRequirement(toolName, args); handled {
			return required, action
		}
		switch normalizeApprovalToken(toolName) {
		case normalizeApprovalToken(ToolNameWrite),
			normalizeApprovalToken(ToolNameEdit),
			normalizeApprovalToken(ToolNameApplyPatch):
			path := strings.ToLower(maputil.StringArg(args, "path"))
			if path == "" {
				return true, "workspace"
			}
			return true, "workspace"
		}
		return true, ""
	}
}
