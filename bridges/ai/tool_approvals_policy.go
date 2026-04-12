package ai

import (
	"strings"

	"github.com/beeper/agentremote/pkg/shared/maputil"
)

func (oc *AIClient) builtinToolApprovalRequirement(toolName string, args map[string]any) (required bool, action string) {
	if oc == nil || !oc.toolApprovalsRuntimeEnabled() {
		return false, ""
	}
	toolName = strings.TrimSpace(toolName)
	if toolName == "" || !oc.toolApprovalsRequireForTool(toolName) {
		return false, ""
	}
	switch toolName {
	case ToolNameMessage:
		action = normalizeMessageAction(maputil.StringArg(args, "action"))
		switch action {
		// Read-only / non-destructive actions (do not require approval).
		case "search",
			// Desktop API read-only surface (agentremote message tool actions).
			"desktop-list-chats", "desktop-search-chats", "desktop-search-messages", "desktop-download-asset":
			return false, action
		default:
			return true, action
		}
	default:
		if handled, required, action := oc.integratedToolApprovalRequirement(toolName, args); handled {
			return required, action
		}
		switch toolName {
		case ToolNameWrite, ToolNameEdit, ToolNameApplyPatch:
			return true, "workspace"
		}
		return true, ""
	}
}
