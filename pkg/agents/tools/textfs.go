package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/beeper/ai-bridge/pkg/shared/toolspec"
)

func execUnavailable(name string) func(ctx context.Context, input map[string]any) (*Result, error) {
	return func(ctx context.Context, input map[string]any) (*Result, error) {
		return ErrorResult(name, "tool execution is handled by the connector runtime"), nil
	}
}

type unavailableBuiltinToolSpec struct {
	name        string
	description string
	title       string
	inputSchema map[string]any
}

func newUnavailableBuiltinTool(spec unavailableBuiltinToolSpec) *Tool {
	return &Tool{
		Tool: mcp.Tool{
			Name:        spec.name,
			Description: spec.description,
			Annotations: &mcp.ToolAnnotations{Title: spec.title},
			InputSchema: spec.inputSchema,
		},
		Type:    ToolTypeBuiltin,
		Group:   GroupFS,
		Execute: execUnavailable(spec.name),
	}
}

var (
	ReadTool = newUnavailableBuiltinTool(unavailableBuiltinToolSpec{
		name:        toolspec.ReadName,
		description: toolspec.ReadDescription,
		title:       "Read",
		inputSchema: toolspec.ReadSchema(),
	})
	WriteTool = newUnavailableBuiltinTool(unavailableBuiltinToolSpec{
		name:        toolspec.WriteName,
		description: toolspec.WriteDescription,
		title:       "Write",
		inputSchema: toolspec.WriteSchema(),
	})
	EditTool = newUnavailableBuiltinTool(unavailableBuiltinToolSpec{
		name:        toolspec.EditName,
		description: toolspec.EditDescription,
		title:       "Edit",
		inputSchema: toolspec.EditSchema(),
	})
)
