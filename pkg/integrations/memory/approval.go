package memory

import "strings"

const (
	RootPath = "memory/"
	FilePath = "memory.md"
)

func (i *Integration) ToolApprovalRequirement(toolName string, args map[string]any) (handled bool, required bool, action string) {
	if i == nil {
		return false, false, ""
	}
	name := strings.ToLower(strings.TrimSpace(toolName))
	switch name {
	case "write", "edit", "apply_patch":
		path := strings.ToLower(strings.TrimSpace(readStringArg(args, "path")))
		if IsManagedPath(path) {
			return true, false, "memory"
		}
		return false, false, ""
	default:
		return false, false, ""
	}
}

func IsManagedPath(path string) bool {
	trimmed := strings.TrimSpace(strings.ToLower(path))
	if trimmed == "" {
		return false
	}
	return trimmed == FilePath || strings.HasPrefix(trimmed, RootPath)
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
