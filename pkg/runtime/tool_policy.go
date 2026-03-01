package runtime

import "strings"

type ToolPolicyInput struct {
	ToolName      string
	ToolKind      string // builtin|mcp|provider
	CallID        string
	RequireForMCP bool
	RequiredTools map[string]struct{}
}

func DecideToolApproval(input ToolPolicyInput) ToolApprovalDecision {
	name := strings.TrimSpace(input.ToolName)
	kind := strings.TrimSpace(strings.ToLower(input.ToolKind))
	decision := ToolApprovalDecision{Tool: name, CallID: input.CallID}

	if kind == "mcp" && input.RequireForMCP {
		decision.State = ToolApprovalRequired
		decision.Reason = "mcp_requires_approval"
		return decision
	}
	if input.RequiredTools != nil {
		if _, ok := input.RequiredTools[name]; ok {
			decision.State = ToolApprovalRequired
			decision.Reason = "tool_in_required_set"
			return decision
		}
	}

	decision.State = ToolApprovalApproved
	decision.Reason = "not_required"
	return decision
}
