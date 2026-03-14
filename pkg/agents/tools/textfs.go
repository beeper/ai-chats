package tools

import "github.com/beeper/agentremote/pkg/shared/toolspec"

const fsUnavailableMsg = "tool execution is handled by the connector runtime"

var (
	ReadTool = newUnavailableTool(
		toolspec.ReadName, toolspec.ReadDescription, "Read",
		toolspec.ReadSchema(), GroupFS, fsUnavailableMsg,
	)
	WriteTool = newUnavailableTool(
		toolspec.WriteName, toolspec.WriteDescription, "Write",
		toolspec.WriteSchema(), GroupFS, fsUnavailableMsg,
	)
	EditTool = newUnavailableTool(
		toolspec.EditName, toolspec.EditDescription, "Edit",
		toolspec.EditSchema(), GroupFS, fsUnavailableMsg,
	)
	ApplyPatchTool = newUnavailableTool(
		toolspec.ApplyPatchName, toolspec.ApplyPatchDescription, "Apply Patch",
		toolspec.ApplyPatchSchema(), GroupFS, fsUnavailableMsg,
	)
)
