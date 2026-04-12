package ai

import (
	"strings"

	"github.com/beeper/agentremote/sdk"
)

func buildBuiltinApprovalPresentation(toolName, action string, args map[string]any) sdk.ApprovalPromptPresentation {
	toolName = strings.TrimSpace(toolName)
	details := make([]sdk.ApprovalDetail, 0, 10)
	if toolName != "" {
		details = append(details, sdk.ApprovalDetail{Label: "Tool", Value: toolName})
	}
	if action = strings.TrimSpace(action); action != "" {
		details = append(details, sdk.ApprovalDetail{Label: "Action", Value: action})
	}
	details = sdk.AppendDetailsFromMap(details, "Arg", args, 8)
	return sdk.BuildApprovalPresentation("Builtin tool request", toolName, details, true)
}

func buildMCPApprovalPresentation(serverLabel, toolName string, input any) sdk.ApprovalPromptPresentation {
	toolName = strings.TrimSpace(toolName)
	details := make([]sdk.ApprovalDetail, 0, 10)
	if serverLabel = strings.TrimSpace(serverLabel); serverLabel != "" {
		details = append(details, sdk.ApprovalDetail{Label: "Server", Value: serverLabel})
	}
	if toolName != "" {
		details = append(details, sdk.ApprovalDetail{Label: "Tool", Value: toolName})
	}
	if inputMap, ok := input.(map[string]any); ok && len(inputMap) > 0 {
		details = sdk.AppendDetailsFromMap(details, "Input", inputMap, 8)
	} else if summary := sdk.ValueSummary(input); summary != "" {
		details = append(details, sdk.ApprovalDetail{Label: "Input", Value: summary})
	}
	return sdk.BuildApprovalPresentation("MCP tool request", toolName, details, true)
}
