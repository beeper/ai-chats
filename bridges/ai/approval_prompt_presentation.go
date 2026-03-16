package ai

import (
	"strings"

	"github.com/beeper/agentremote"
)

func buildBuiltinApprovalPresentation(toolName, action string, args map[string]any) agentremote.ApprovalPromptPresentation {
	toolName = strings.TrimSpace(toolName)
	action = strings.TrimSpace(action)
	title := "Builtin tool request"
	if toolName != "" {
		title = "Builtin tool request: " + toolName
	}
	details := make([]agentremote.ApprovalDetail, 0, 10)
	if toolName != "" {
		details = append(details, agentremote.ApprovalDetail{Label: "Tool", Value: toolName})
	}
	if action != "" {
		details = append(details, agentremote.ApprovalDetail{Label: "Action", Value: action})
	}
	details = agentremote.AppendDetailsFromMap(details, "Arg", args, 8)
	return agentremote.ApprovalPromptPresentation{
		Title:       title,
		Details:     details,
		AllowAlways: true,
	}
}

func buildMCPApprovalPresentation(serverLabel, toolName string, input any) agentremote.ApprovalPromptPresentation {
	serverLabel = strings.TrimSpace(serverLabel)
	toolName = strings.TrimSpace(toolName)
	title := "MCP tool request"
	if toolName != "" {
		title = "MCP tool request: " + toolName
	}
	details := make([]agentremote.ApprovalDetail, 0, 10)
	if serverLabel != "" {
		details = append(details, agentremote.ApprovalDetail{Label: "Server", Value: serverLabel})
	}
	if toolName != "" {
		details = append(details, agentremote.ApprovalDetail{Label: "Tool", Value: toolName})
	}
	if inputMap, ok := input.(map[string]any); ok && len(inputMap) > 0 {
		details = agentremote.AppendDetailsFromMap(details, "Input", inputMap, 8)
	} else if summary := agentremote.ValueSummary(input); summary != "" {
		details = append(details, agentremote.ApprovalDetail{Label: "Input", Value: summary})
	}
	return agentremote.ApprovalPromptPresentation{
		Title:       title,
		Details:     details,
		AllowAlways: true,
	}
}
