package tools

import "github.com/beeper/ai-bridge/pkg/shared/toolspec"

var ApplyPatchTool = newUnavailableBuiltinTool(unavailableBuiltinToolSpec{
	name:        toolspec.ApplyPatchName,
	description: toolspec.ApplyPatchDescription,
	title:       "apply_patch",
	inputSchema: toolspec.ApplyPatchSchema(),
})
