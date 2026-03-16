package memory

import (
	"strings"

	"github.com/beeper/agentremote/pkg/shared/maputil"
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
	if path == "" {
		return false
	}
	return path == FilePath || strings.HasPrefix(path, RootPath)
}
