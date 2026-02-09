package tools

import (
	"sync"

	"github.com/beeper/ai-bridge/pkg/agents/toolpolicy"
)

var (
	toolLookupOnce    sync.Once
	builtinToolByName map[string]*Tool
	allToolByName     map[string]*Tool
)

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
	GroupNexus     = toolpolicy.GroupNexus
)

// BuiltinTools returns all locally-executable builtin tools.
func BuiltinTools() []*Tool {
	tools := []*Tool{
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
		BeeperDocsTool,
		ReadTool,
		ApplyPatchTool,
		WriteTool,
		EditTool,
	}
	tools = append(tools, NexusTools()...)
	return tools
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

	// Register all tools
	for _, tool := range AllTools() {
		reg.Register(tool)
	}

	return reg
}

// BuiltinRegistry returns a registry with only builtin tools.
func BuiltinRegistry() *Registry {
	reg := NewRegistry()

	for _, tool := range BuiltinTools() {
		reg.Register(tool)
	}

	return reg
}

// GetBuiltinTool returns a builtin tool by name.
func GetBuiltinTool(name string) *Tool {
	toolLookupOnce.Do(initToolLookup)
	return builtinToolByName[name]
}

// GetTool returns any tool by name (builtin or provider).
func GetTool(name string) *Tool {
	toolLookupOnce.Do(initToolLookup)
	return allToolByName[name]
}

func initToolLookup() {
	builtinToolByName = make(map[string]*Tool)
	for _, tool := range BuiltinTools() {
		if _, exists := builtinToolByName[tool.Name]; !exists {
			builtinToolByName[tool.Name] = tool
		}
	}

	allToolByName = make(map[string]*Tool)
	for _, tool := range AllTools() {
		if _, exists := allToolByName[tool.Name]; !exists {
			allToolByName[tool.Name] = tool
		}
	}
}
