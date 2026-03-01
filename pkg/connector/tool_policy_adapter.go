package connector

import (
	"strings"

	airuntime "github.com/beeper/ai-bridge/pkg/runtime"
)

func toRuntimeToolApprovalDecision(required bool, toolKind, toolName, callID string, requireForMCP bool) airuntime.ToolApprovalDecision {
	input := airuntime.ToolPolicyInput{
		ToolName:      strings.TrimSpace(toolName),
		ToolKind:      strings.TrimSpace(toolKind),
		CallID:        strings.TrimSpace(callID),
		RequireForMCP: requireForMCP,
	}
	if required {
		input.RequiredTools = map[string]struct{}{strings.TrimSpace(toolName): {}}
	}
	return airuntime.DecideToolApproval(input)
}
