package agents

import "errors"

// Agent-related errors.
var (
	ErrMissingAgentID   = errors.New("agent ID is required")
	ErrMissingAgentName = errors.New("agent name is required")
	ErrAgentNotFound    = errors.New("agent not found")
	ErrAgentIsPreset    = errors.New("cannot modify preset agent")
)
