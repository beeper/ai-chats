package tools

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/beeper/ai-bridge/pkg/shared/toolspec"
)

// AgentsListTool lists agent ids allowed for sessions_spawn.
var AgentsListTool = &Tool{
	Tool: mcp.Tool{
		Name:        "agents_list",
		Description: "List agent ids you can target with sessions_spawn (based on allowlists).",
		Annotations: &mcp.ToolAnnotations{Title: "Agents List"},
		InputSchema: toolspec.EmptyObjectSchema(),
	},
	Type:  ToolTypeBuiltin,
	Group: GroupSessions,
}
