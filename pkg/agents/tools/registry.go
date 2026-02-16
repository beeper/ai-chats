package tools

import (
	"cmp"
	"slices"
	"sync"
)

// Registry manages available tools with grouping and aliasing support.
type Registry struct {
	mu     sync.RWMutex
	tools  map[string]*Tool    // name -> tool
	groups map[string][]string // group name -> tool names
}

// NewRegistry creates a new tool registry.
func NewRegistry() *Registry {
	return &Registry{
		tools:  make(map[string]*Tool),
		groups: make(map[string][]string),
	}
}

// Register adds a tool to the registry.
func (r *Registry) Register(tool *Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	name := tool.Name
	r.tools[name] = tool

	// Add to group if specified
	if tool.Group != "" {
		r.groups[tool.Group] = append(r.groups[tool.Group], name)
	}
}

// All returns all registered tools.
func (r *Registry) All() []*Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	tools := make([]*Tool, 0, len(r.tools))
	for _, tool := range r.tools {
		tools = append(tools, tool)
	}

	// Sort by name for consistent ordering
	slices.SortFunc(tools, func(a, b *Tool) int {
		return cmp.Compare(a.Name, b.Name)
	})

	return tools
}
