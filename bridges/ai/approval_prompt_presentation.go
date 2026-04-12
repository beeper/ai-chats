package ai

import (
	"strings"

	"github.com/beeper/agentremote/sdk"
)

func buildBuiltinApprovalPresentation(toolName, action string, args map[string]any) sdk.ApprovalPromptPresentation {
	toolName = strings.TrimSpace(toolName)
	action = strings.TrimSpace(action)
	title := "Builtin tool request"
	if toolName != "" {
		title = "Builtin tool request: " + toolName
	}
	details := make([]sdk.ApprovalDetail, 0, 10)
	if toolName != "" {
		details = append(details, sdk.ApprovalDetail{Label: "Tool", Value: toolName})
	}
	if action != "" {
		details = append(details, sdk.ApprovalDetail{Label: "Action", Value: action})
	}
	details = sdk.AppendDetailsFromMap(details, "Arg", args, 8)
	return sdk.ApprovalPromptPresentation{
		Title:       title,
		Details:     details,
		AllowAlways: true,
	}
}

func buildMCPApprovalPresentation(serverLabel, toolName string, input any) sdk.ApprovalPromptPresentation {
	serverLabel = strings.TrimSpace(serverLabel)
	toolName = strings.TrimSpace(toolName)
	title := "MCP tool request"
	if toolName != "" {
		title = "MCP tool request: " + toolName
	}
	details := make([]sdk.ApprovalDetail, 0, 10)
	if serverLabel != "" {
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
	return sdk.ApprovalPromptPresentation{
		Title:       title,
		Details:     details,
		AllowAlways: true,
	}
}
