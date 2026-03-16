package tools

import (
	"context"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/beeper/agentremote/pkg/agents/toolpolicy"
)

var toolLookup = sync.OnceValue(func() map[string]*Tool {
	all := AllTools()
	m := make(map[string]*Tool, len(all))
	for _, tool := range all {
		m[tool.Name] = tool
	}
	return m
})

// Tool group constants for policy composition (OpenClaw-style shorthands).
const (
	GroupSearch    = toolpolicy.GroupSearch
	GroupCalc      = toolpolicy.GroupCalc
	GroupBuilder   = toolpolicy.GroupBuilder
	GroupMessaging = toolpolicy.GroupMessaging
	GroupSessions  = toolpolicy.GroupSessions
	GroupMemory    = toolpolicy.GroupMemory
	GroupWeb       = toolpolicy.GroupWeb
	GroupMedia     = toolpolicy.GroupMedia
	GroupStatus    = toolpolicy.GroupStatus
	GroupOpenClaw  = toolpolicy.GroupOpenClaw
	GroupFS        = toolpolicy.GroupFS
)

// BuiltinTools returns all locally-executable builtin tools.
func BuiltinTools() []*Tool {
	return []*Tool{
		Calculator,
		WebSearch,
		MessageTool,
		CronTool,
		WebFetchTool,
		SessionStatusTool,
		MemorySearchTool,
		MemoryGetTool,
		ImageTool,
		ImageGenerateTool,
		TTSTool,
		GravatarFetchTool,
		GravatarSetTool,
		BeeperDocsTool,
		BeeperSendFeedbackTool,
		ReadTool,
		ApplyPatchTool,
		WriteTool,
		EditTool,
	}
}

// AllTools returns all tools (builtin + provider markers).
func AllTools() []*Tool {
	seen := make(map[string]struct{})
	var all []*Tool
	appendTools := func(list []*Tool) {
		for _, tool := range list {
			if tool == nil || tool.Name == "" {
				continue
			}
			if _, ok := seen[tool.Name]; ok {
				continue
			}
			seen[tool.Name] = struct{}{}
			all = append(all, tool)
		}
	}
	appendTools(BuiltinTools())
	appendTools(SessionTools())
	appendTools(BossTools())
	return all
}

// DefaultRegistry returns a registry with all default tools registered.
func DefaultRegistry() *Registry {
	reg := NewRegistry()
	for _, tool := range AllTools() {
		reg.Register(tool)
	}
	return reg
}

// GetTool returns any tool by name (builtin or provider).
func GetTool(name string) *Tool {
	return toolLookup()[name]
}

func newBuiltinTool(name, description, title string, schema map[string]any, group string, execute func(context.Context, map[string]any) (*Result, error)) *Tool {
	return &Tool{
		Tool: mcp.Tool{
			Name:        name,
			Description: description,
			Annotations: &mcp.ToolAnnotations{Title: title},
			InputSchema: schema,
		},
		Type:    ToolTypeBuiltin,
		Group:   group,
		Execute: execute,
	}
}
