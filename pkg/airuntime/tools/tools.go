package tools

import (
	"context"
	"errors"
	"fmt"

	agenttools "github.com/beeper/ai-bridge/pkg/agents/tools"
)

// Executor provides a transport-agnostic way to execute tools from the shared registry.
// Higher layers (bridge/bot) provide policy + per-room enablement checks.
type Executor struct {
	Registry *agenttools.Registry
}

func (e *Executor) Execute(ctx context.Context, toolName string, input map[string]any) (*agenttools.Result, error) {
	if e == nil || e.Registry == nil {
		return nil, errors.New("missing tool registry")
	}
	t := e.Registry.Get(toolName)
	if t == nil {
		return nil, fmt.Errorf("unknown tool: %s", toolName)
	}
	if t.Execute == nil {
		return nil, fmt.Errorf("tool %s has no local executor", toolName)
	}
	return t.Execute(ctx, input)
}
