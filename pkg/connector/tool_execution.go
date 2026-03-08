package connector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/ai-bridge/pkg/agents/toolpolicy"
	"github.com/beeper/ai-bridge/pkg/agents/tools"
)

// activeToolCall tracks a tool call that's in progress
type activeToolCall struct {
	callID      string
	toolName    string
	toolType    ToolType
	input       strings.Builder
	startedAtMs int64
	eventID     id.EventID // Event ID of the tool call timeline event
	result      string     // Result from tool execution (for continuation)
	itemID      string     // Item ID from the stream event (used as call_id for continuation)
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

func toolDisplayTitle(toolName string) string {
	toolName = normalizeToolAlias(toolName)
	if t := tools.GetTool(toolName); t != nil && t.Annotations != nil && t.Annotations.Title != "" {
		return t.Annotations.Title
	}
	return toolName
}

// sendToolCallEvent intentionally does not emit a separate timeline projection.
// The canonical transport is UIMessage plus stream events; callers still expect an
// event ID return value, so this remains as a no-op compatibility stub.
func (oc *AIClient) sendToolCallEvent(ctx context.Context, portal *bridgev2.Portal, state *streamingState, tool *activeToolCall) id.EventID {
	_ = ctx
	_ = portal
	_ = state
	_ = tool
	return ""
}

// sendToolResultEvent intentionally does not emit a separate timeline projection.
// The canonical transport is UIMessage plus stream events; callers still expect an
// event ID return value, so this remains as a no-op compatibility stub.
func (oc *AIClient) sendToolResultEvent(ctx context.Context, portal *bridgev2.Portal, state *streamingState, tool *activeToolCall, result string, resultStatus ResultStatus) id.EventID {
	_ = ctx
	_ = portal
	_ = state
	_ = tool
	_ = result
	_ = resultStatus
	return ""
}

// executeBuiltinTool finds and executes a builtin tool by name.
// For Builder rooms, this also handles boss agent tools. Session tools are handled for all rooms.
func (oc *AIClient) executeBuiltinTool(ctx context.Context, portal *bridgev2.Portal, toolName string, argsJSON string) (string, error) {
	argsJSON = normalizeToolArgsJSON(argsJSON)
	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid tool arguments: %w", err)
	}
	meta := (*PortalMetadata)(nil)
	if portal != nil {
		meta = portalMeta(portal)
	}
	if handled, result, err := oc.executeIntegratedTool(ctx, portal, meta, normalizeToolAlias(toolName), args, argsJSON); handled {
		return result, err
	}
	return oc.executeBuiltinToolDirect(ctx, portal, toolName, argsJSON)
}

func (oc *AIClient) executeBuiltinToolDirect(ctx context.Context, portal *bridgev2.Portal, toolName string, argsJSON string) (string, error) {
	argsJSON = normalizeToolArgsJSON(argsJSON)
	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid tool arguments: %w", err)
	}

	toolName = normalizeToolAlias(toolName)

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
	// Create boss tool executor with store adapter
	store := NewBossStoreAdapter(oc)
	executor := tools.NewBossToolExecutor(store)

	var result *tools.Result
	var err error

	if toolName == "run_internal_command" {
		if roomID, ok := args["room_id"].(string); !ok || strings.TrimSpace(roomID) == "" {
			if portal != nil && portal.MXID != "" {
				args["room_id"] = portal.MXID.String()
			}
		}
	}
	type sessionToolFunc func(context.Context, *bridgev2.Portal, map[string]any) (*tools.Result, error)
	sessionTools := map[string]sessionToolFunc{
		"sessions_spawn":   oc.executeSessionsSpawn,
		"sessions_list":    oc.executeSessionsList,
		"sessions_history": oc.executeSessionsHistory,
		"sessions_send":    oc.executeSessionsSend,
		"agents_list":      oc.executeAgentsList,
	}
	if fn, ok := sessionTools[toolName]; ok {
		result, err = fn(ctx, portal, args)
		return bossToolResultFromToolsResult(result, err)
	}

	switch toolName {
	case "create_agent":
		result, err = executor.ExecuteCreateAgent(ctx, args)
	case "fork_agent":
		result, err = executor.ExecuteForkAgent(ctx, args)
	case "edit_agent":
		result, err = executor.ExecuteEditAgent(ctx, args)
	case "delete_agent":
		result, err = executor.ExecuteDeleteAgent(ctx, args)
	case "list_agents":
		result, err = executor.ExecuteListAgents(ctx, args)
	case "list_models":
		result, err = executor.ExecuteListModels(ctx, args)
	case "run_internal_command":
		result, err = executor.ExecuteRunInternalCommand(ctx, args)
	case "modify_room":
		result, err = executor.ExecuteModifyRoom(ctx, args)
	default:
		return nil // Not a boss tool
	}

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
