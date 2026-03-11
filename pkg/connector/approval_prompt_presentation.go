package connector

import (
	"strings"

	"github.com/beeper/agentremote/pkg/bridgeadapter"
)

func buildBuiltinApprovalPresentation(toolName, action string, args map[string]any) bridgeadapter.ApprovalPromptPresentation {
	toolName = strings.TrimSpace(toolName)
	action = strings.TrimSpace(action)
	title := "Builtin tool request"
	if toolName != "" {
		title = "Builtin tool request: " + toolName
	}
	details := make([]bridgeadapter.ApprovalDetail, 0, 10)
	if toolName != "" {
		details = append(details, bridgeadapter.ApprovalDetail{Label: "Tool", Value: toolName})
	}
	if action != "" {
		details = append(details, bridgeadapter.ApprovalDetail{Label: "Action", Value: action})
	}
	details = bridgeadapter.AppendDetailsFromMap(details, "Arg", args, 8)
	return bridgeadapter.ApprovalPromptPresentation{
		Title:       title,
		Details:     details,
		AllowAlways: true,
	}
}

func buildMCPApprovalPresentation(serverLabel, toolName string, input any) bridgeadapter.ApprovalPromptPresentation {
	serverLabel = strings.TrimSpace(serverLabel)
	toolName = strings.TrimSpace(toolName)
	title := "MCP tool request"
	if toolName != "" {
		title = "MCP tool request: " + toolName
	}
	details := make([]bridgeadapter.ApprovalDetail, 0, 10)
	if serverLabel != "" {
		details = append(details, bridgeadapter.ApprovalDetail{Label: "Server", Value: serverLabel})
	}
	if toolName != "" {
		details = append(details, bridgeadapter.ApprovalDetail{Label: "Tool", Value: toolName})
	}
	if inputMap, ok := input.(map[string]any); ok && len(inputMap) > 0 {
		details = bridgeadapter.AppendDetailsFromMap(details, "Input", inputMap, 8)
	} else if summary := bridgeadapter.ValueSummary(input); summary != "" {
		details = append(details, bridgeadapter.ApprovalDetail{Label: "Input", Value: summary})
	}
	return bridgeadapter.ApprovalPromptPresentation{
		Title:       title,
		Details:     details,
		AllowAlways: true,
	}
}
