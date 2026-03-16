package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote/pkg/agents/toolpolicy"
	"github.com/beeper/agentremote/pkg/agents/tools"
	"github.com/beeper/agentremote/pkg/shared/streamui"
)

// activeToolCall tracks a tool call that's in progress
type activeToolCall struct {
	registryKey string
	callID      string
	approvalID  string
	toolName    string
	toolType    ToolType
	input       strings.Builder
	startedAtMs int64
	result      string // Result from tool execution (for continuation)
	itemID      string // Item ID from the stream event (used as call_id for continuation)
}

func normalizeToolArgsJSON(argsJSON string) string {
	trimmed := strings.TrimSpace(argsJSON)
	if trimmed == "" || trimmed == "null" {
		return "{}"
	}
	return trimmed
}

func parseToolInputPayload(argsJSON string) map[string]any {
	trimmed := strings.TrimSpace(argsJSON)
	if trimmed == "" {
		return nil
	}
	var parsed any
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return map[string]any{"_raw": trimmed}
	}
	if m, ok := parsed.(map[string]any); ok {
		return m
	}
	return map[string]any{"value": parsed}
}

// toolDisplayTitle is an alias for streamui.ToolDisplayTitle.
var toolDisplayTitle = streamui.ToolDisplayTitle

// parseToolArgs normalizes and parses tool arguments JSON into a map.
func parseToolArgs(argsJSON string) (string, map[string]any, error) {
	argsJSON = normalizeToolArgsJSON(argsJSON)
	var parsed any
	if err := json.Unmarshal([]byte(argsJSON), &parsed); err != nil {
		return "", nil, fmt.Errorf("invalid tool arguments: %w", err)
	}
	args, ok := parsed.(map[string]any)
	if !ok {
		return argsJSON, nil, nil
	}
	return argsJSON, args, nil
}

// executeBuiltinTool finds and executes a builtin tool by name.
// For Builder rooms, this also handles boss agent tools. Session tools are handled for all rooms.
func (oc *AIClient) executeBuiltinTool(ctx context.Context, portal *bridgev2.Portal, toolName string, argsJSON string) (string, error) {
	toolName = strings.TrimSpace(toolName)
	if toolpolicy.IsOwnerOnlyToolName(toolName) {
		senderID := ""
		if btc := GetBridgeToolContext(ctx); btc != nil {
			senderID = btc.SenderID
		}
		var cfg *Config
		if oc != nil && oc.connector != nil {
			cfg = &oc.connector.Config
		}
		if !isOwnerAllowed(cfg, senderID) {
			return "", errors.New("tool restricted to owner senders")
		}
	}
	argsJSON, args, err := parseToolArgs(argsJSON)
	if err != nil {
		return "", err
	}
	execArgs := args
	if execArgs == nil {
		execArgs = parseToolInputPayload(argsJSON)
	}
	var meta *PortalMetadata
	if portal != nil {
		meta = portalMeta(portal)
	}
	if handled, result, err := oc.executeIntegratedTool(ctx, portal, meta, toolName, args, argsJSON); handled {
		return result, err
	}
	return oc.executeBuiltinToolDirect(ctx, portal, toolName, execArgs)
}

func (oc *AIClient) executeBuiltinToolDirect(ctx context.Context, portal *bridgev2.Portal, toolName string, args map[string]any) (string, error) {
	toolName = strings.TrimSpace(toolName)

	if toolpolicy.IsOwnerOnlyToolName(toolName) {
		senderID := ""
		if btc := GetBridgeToolContext(ctx); btc != nil {
			senderID = btc.SenderID
		}
		if !isOwnerAllowed(&oc.connector.Config, senderID) {
			return "", errors.New("tool restricted to owner senders")
		}
	}

	var meta *PortalMetadata
	if portal != nil {
		meta = portalMeta(portal)
	}

	// Route MCP tools through the MCP bridge when configured.
	if oc.shouldUseMCPTool(ctx, toolName) {
		return oc.executeMCPTool(ctx, toolName, args)
	}
	// Check if this is a Boss room or a session tool - use boss tool executor
	if (meta != nil && hasBossAgent(meta)) || tools.IsSessionTool(toolName) || tools.IsBossTool(toolName) {
		if result := oc.executeBossTool(ctx, portal, toolName, args); result != nil {
			return result.Content, result.Error
		}
	}

	// Standard builtin tools
	if tool := GetBuiltinTool(toolName); tool != nil {
		return tool.Execute(ctx, args)
	}
	return "", fmt.Errorf("unknown tool: %s", toolName)
}

// bossToolResult holds the result from a boss tool execution.
type bossToolResult struct {
	Content string
	Error   error
}

// executeBossTool attempts to execute a boss agent tool.
// Returns nil if the tool is not a boss tool.
func (oc *AIClient) executeBossTool(ctx context.Context, portal *bridgev2.Portal, toolName string, args map[string]any) *bossToolResult {
	// Session tools are handled by the client directly.
	type sessionToolFunc func(context.Context, *bridgev2.Portal, map[string]any) (*tools.Result, error)
	sessionTools := map[string]sessionToolFunc{
		"sessions_spawn":   oc.executeSessionsSpawn,
		"sessions_list":    oc.executeSessionsList,
		"sessions_history": oc.executeSessionsHistory,
		"sessions_send":    oc.executeSessionsSend,
		"agents_list":      oc.executeAgentsList,
	}
	if fn, ok := sessionTools[toolName]; ok {
		result, err := fn(ctx, portal, args)
		return bossToolResultFromToolsResult(result, err)
	}

	// Boss executor tools share a common pattern.
	store := NewBossStoreAdapter(oc)
	executor := tools.NewBossToolExecutor(store)

	// Default room_id for run_internal_command if not provided.
	if toolName == "run_internal_command" {
		if roomID, ok := args["room_id"].(string); !ok || strings.TrimSpace(roomID) == "" {
			if portal != nil && portal.MXID != "" {
				args["room_id"] = portal.MXID.String()
			}
		}
	}

	type executorFunc func(context.Context, map[string]any) (*tools.Result, error)
	executorTools := map[string]executorFunc{
		"create_agent":         executor.ExecuteCreateAgent,
		"fork_agent":           executor.ExecuteForkAgent,
		"edit_agent":           executor.ExecuteEditAgent,
		"delete_agent":         executor.ExecuteDeleteAgent,
		"list_agents":          executor.ExecuteListAgents,
		"list_models":          executor.ExecuteListModels,
		"run_internal_command": executor.ExecuteRunInternalCommand,
		"modify_room":          executor.ExecuteModifyRoom,
	}
	fn, ok := executorTools[toolName]
	if !ok {
		return nil // Not a boss tool
	}
	result, err := fn(ctx, args)
	return bossToolResultFromToolsResult(result, err)
}

func bossToolResultFromToolsResult(result *tools.Result, err error) *bossToolResult {
	if err != nil {
		return &bossToolResult{Error: err}
	}
	if result == nil {
		return &bossToolResult{Content: ""}
	}
	content := result.Text()
	if result.Status == tools.ResultError {
		return &bossToolResult{Error: errors.New(content)}
	}
	return &bossToolResult{Content: content}
}
