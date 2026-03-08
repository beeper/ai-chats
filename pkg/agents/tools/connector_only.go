package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func newConnectorOnlyTool(name, description, title string, schema map[string]any) *Tool {
	return &Tool{
		Tool: mcp.Tool{
			Name:        name,
			Description: description,
			Annotations: &mcp.ToolAnnotations{Title: title},
			InputSchema: schema,
		},
		Type:    ToolTypeBuiltin,
		Group:   GroupWeb,
		Execute: connectorOnlyPlaceholder(name),
	}
}

func connectorOnlyPlaceholder(toolName string) func(context.Context, map[string]any) (*Result, error) {
	return func(_ context.Context, _ map[string]any) (*Result, error) {
		return ErrorResult(toolName, toolName+" is only available through the connector"), nil
	}
}
