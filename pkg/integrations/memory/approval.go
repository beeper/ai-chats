package memory

import (
	"strings"

	"github.com/beeper/ai-bridge/pkg/shared/maputil"
)

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
		path := strings.ToLower(strings.TrimSpace(maputil.StringArg(args, "path")))
		if isManagedPath(path) {
			return true, false, "memory"
		}
		return false, false, ""
	default:
		return false, false, ""
	}
}

func isManagedPath(path string) bool {
	trimmed := strings.TrimSpace(strings.ToLower(path))
	if trimmed == "" {
		return false
	}
	return trimmed == FilePath || strings.HasPrefix(trimmed, RootPath)
}
