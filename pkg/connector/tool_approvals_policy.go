package connector

import "strings"

func readStringArgAny(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	if v, ok := args[key]; ok {
		if s, ok := v.(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

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
		action = normalizeMessageAction(readStringArgAny(args, "action"))
		switch action {
		// Read-only / non-destructive actions (do not require approval).
		case "reactions", "search", "read", "member-info", "channel-info", "list-pins",
			// Desktop API read-only surface (ai-bridge message tool actions).
			"desktop-list-chats", "desktop-search-chats", "desktop-search-messages", "desktop-download-asset":
			return false, action
		default:
			return true, action
		}
	case normalizeApprovalToken(ToolNameWrite),
		normalizeApprovalToken(ToolNameEdit),
		normalizeApprovalToken(ToolNameApplyPatch):
		path := strings.ToLower(readStringArgAny(args, "path"))
		// Memory writes/edits are lower-risk (bridge-local notes/state), so allow without approval.
		if path == "memory.md" || strings.HasPrefix(path, "memory/") {
			return false, "memory"
		}
		// If we don't know the path (e.g. apply_patch), default to requiring approval.
		if path == "" {
			return true, "workspace"
		}
		return true, "workspace"
	case normalizeApprovalToken(ToolNameCron):
		action = normalizeApprovalToken(readStringArgAny(args, "action"))
		switch action {
		case "status", "list", "runs":
			return false, action
		default:
			return true, action
		}
	default:
		return true, ""
	}
}
