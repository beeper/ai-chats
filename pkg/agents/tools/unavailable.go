package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/beeper/agentremote/pkg/shared/toolspec"
)

// newUnavailableTool creates a builtin tool whose Execute returns an error
// explaining that actual execution is handled elsewhere (connector, runtime, etc.).
func newUnavailableTool(name, description, title string, schema map[string]any, group, errMsg string) *Tool {
	return &Tool{
		Tool: mcp.Tool{
			Name:        name,
			Description: description,
			Annotations: &mcp.ToolAnnotations{Title: title},
			InputSchema: schema,
		},
		Type:  toolspec.ToolTypeBuiltin,
		Group: group,
		Execute: func(_ context.Context, _ map[string]any) (*Result, error) {
			return ErrorResult(name, errMsg), nil
		},
	}
}
