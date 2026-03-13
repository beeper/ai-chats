package tools

import "github.com/beeper/agentremote/pkg/shared/toolspec"

var ApplyPatchTool = newUnavailableTool(
	toolspec.ApplyPatchName, toolspec.ApplyPatchDescription, "Apply Patch",
	toolspec.ApplyPatchSchema(), GroupFS, fsUnavailableMsg,
)
