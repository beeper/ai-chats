package tools

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/beeper/ai-bridge/pkg/shared/toolspec"
)

var CronTool = &Tool{
	Tool: mcp.Tool{
		Name:        toolspec.CronName,
		Description: toolspec.CronDescription,
		Annotations: &mcp.ToolAnnotations{Title: "Scheduler"},
		InputSchema: toolspec.CronSchema(),
	},
	Type:  ToolTypeBuiltin,
	Group: GroupOpenClaw,
}
