package connector

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/openai/openai-go/v3/responses"
	"maunium.net/go/mautrix/bridgev2"
)

func (oc *AIClient) upsertActiveToolFromDescriptor(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	activeTools map[string]*activeToolCall,
	desc responseToolDescriptor,
) *activeToolCall {
	if activeTools == nil || strings.TrimSpace(desc.itemID) == "" || strings.TrimSpace(desc.callID) == "" {
		return nil
	}
	tool, ok := activeTools[desc.itemID]
	if !ok || tool == nil {
		tool = &activeToolCall{
			callID:      SanitizeToolCallID(desc.callID, "strict"),
			toolName:    desc.toolName,
			toolType:    desc.toolType,
			startedAtMs: time.Now().UnixMilli(),
			itemID:      desc.itemID,
		}
		activeTools[desc.itemID] = tool
	}
	if strings.TrimSpace(desc.callID) != "" {
		tool.callID = SanitizeToolCallID(desc.callID, "strict")
	}
	if strings.TrimSpace(desc.toolName) != "" {
		tool.toolName = desc.toolName
	}
	if desc.toolType != "" {
		tool.toolType = desc.toolType
	}
	state.uiToolNameByToolCallID[tool.callID] = tool.toolName
	state.uiToolTypeByToolCallID[tool.callID] = tool.toolType

	if tool.eventID == "" && strings.TrimSpace(tool.toolName) != "" {
		tool.eventID = oc.sendToolCallEvent(ctx, portal, state, tool)
	}
	oc.ensureUIToolInputStart(ctx, portal, state, tool.callID, tool.toolName, desc.providerExecuted, desc.dynamic, toolDisplayTitle(tool.toolName), nil)
	return tool
}

func (oc *AIClient) ensureActiveToolForStreamItem(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	activeTools map[string]*activeToolCall,
	itemID string,
	item responses.ResponseOutputItemUnion,
) *activeToolCall {
	if activeTools == nil || state == nil {
		return nil
	}
	if tool, exists := activeTools[itemID]; exists {
		return tool
	}
	itemDesc := deriveToolDescriptorForOutputItem(item, state)
	if !itemDesc.ok {
		return nil
	}
	return oc.upsertActiveToolFromDescriptor(ctx, portal, state, activeTools, itemDesc)
}

func (oc *AIClient) handleCustomToolInputDeltaFromOutputItem(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	activeTools map[string]*activeToolCall,
	itemID string,
	item responses.ResponseOutputItemUnion,
	delta string,
) {
	tool := oc.ensureActiveToolForStreamItem(ctx, portal, state, activeTools, itemID, item)
	if tool == nil {
		return
	}
	tool.input.WriteString(delta)
	oc.emitUIToolInputDelta(ctx, portal, state, tool.callID, tool.toolName, delta, tool.toolType == ToolTypeProvider)
}

func (oc *AIClient) handleCustomToolInputDoneFromOutputItem(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	activeTools map[string]*activeToolCall,
	itemID string,
	item responses.ResponseOutputItemUnion,
	inputText string,
) {
	tool := oc.ensureActiveToolForStreamItem(ctx, portal, state, activeTools, itemID, item)
	if tool == nil {
		return
	}
	if tool.input.Len() == 0 && strings.TrimSpace(inputText) != "" {
		tool.input.WriteString(inputText)
	}
	oc.emitUIToolInputAvailable(ctx, portal, state, tool.callID, tool.toolName, parseJSONOrRaw(tool.input.String()), tool.toolType == ToolTypeProvider)
}

func (oc *AIClient) handleProviderToolInputDeltaFromOutputItem(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	activeTools map[string]*activeToolCall,
	itemID string,
	item responses.ResponseOutputItemUnion,
	delta string,
) {
	tool := oc.ensureActiveToolForStreamItem(ctx, portal, state, activeTools, itemID, item)
	if tool == nil {
		return
	}
	tool.input.WriteString(delta)
	oc.emitUIToolInputDelta(ctx, portal, state, tool.callID, tool.toolName, delta, true)
}

func (oc *AIClient) handleProviderToolInputDoneFromOutputItem(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	activeTools map[string]*activeToolCall,
	itemID string,
	item responses.ResponseOutputItemUnion,
	inputText string,
) {
	tool := oc.ensureActiveToolForStreamItem(ctx, portal, state, activeTools, itemID, item)
	if tool == nil {
		return
	}
	if tool.input.Len() == 0 && strings.TrimSpace(inputText) != "" {
		tool.input.WriteString(inputText)
	}
	oc.emitUIToolInputAvailable(ctx, portal, state, tool.callID, tool.toolName, parseJSONOrRaw(tool.input.String()), true)
}

func (oc *AIClient) handleMCPCallFailedFromOutputItem(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	activeTools map[string]*activeToolCall,
	itemID string,
	item responses.ResponseOutputItemUnion,
) {
	tool := oc.ensureActiveToolForStreamItem(ctx, portal, state, activeTools, itemID, item)
	if tool == nil {
		return
	}
	if state != nil && state.uiToolOutputFinalized[tool.callID] {
		return
	}
	errorText := strings.TrimSpace(item.Error)
	if errorText == "" {
		errorText = "MCP tool call failed"
	}
	denied := outputItemLooksDenied(item)
	if denied {
		oc.emitUIToolOutputDenied(ctx, portal, state, tool.callID)
	} else {
		oc.emitUIToolOutputError(ctx, portal, state, tool.callID, errorText, true)
	}

	output := map[string]any{}
	if denied {
		output["status"] = "denied"
	} else {
		output["error"] = errorText
	}
	resultPayload := errorText
	if denied && resultPayload == "" {
		resultPayload = "Denied"
	}
	resultEventID := oc.sendToolResultEvent(ctx, portal, state, tool, resultPayload, ResultStatusError)
	state.toolCalls = append(state.toolCalls, ToolCallMetadata{
		CallID:        tool.callID,
		ToolName:      tool.toolName,
		ToolType:      string(tool.toolType),
		Output:        output,
		Status:        string(ToolStatusFailed),
		ResultStatus:  string(ResultStatusError),
		ErrorMessage:  errorText,
		StartedAtMs:   tool.startedAtMs,
		CompletedAtMs: time.Now().UnixMilli(),
		CallEventID:   string(tool.eventID),
		ResultEventID: string(resultEventID),
	})
}

// gateMcpToolApproval handles an MCP approval request item: registers the
// approval, auto-approves when policy allows, or emits a UI approval request.
func (oc *AIClient) gateMcpToolApproval(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	tool *activeToolCall,
	desc responseToolDescriptor,
	item responses.ResponseOutputItemUnion,
) {
	approvalID := strings.TrimSpace(item.ID)
	if approvalID == "" {
		approvalID = NewCallID()
	}
	state.uiToolCallIDByApproval[approvalID] = tool.callID
	if tool.input.Len() == 0 {
		tool.input.WriteString(stringifyJSONValue(desc.input))
	}
	oc.emitUIToolInputAvailable(ctx, portal, state, tool.callID, tool.toolName, desc.input, true)
	if state.pendingMcpApprovalsSeen[approvalID] {
		return
	}
	state.pendingMcpApprovalsSeen[approvalID] = true
	parsed := item.AsMcpApprovalRequest()
	serverLabel := strings.TrimSpace(parsed.ServerLabel)
	mcpToolName := strings.TrimSpace(parsed.Name)
	state.pendingMcpApprovals = append(state.pendingMcpApprovals, mcpApprovalRequest{
		approvalID:  approvalID,
		toolCallID:  tool.callID,
		toolName:    tool.toolName,
		serverLabel: serverLabel,
	})
	ttl := time.Duration(oc.toolApprovalsTTLSeconds()) * time.Second
	oc.registerToolApproval(ToolApprovalParams{
		ApprovalID:   approvalID,
		RoomID:       state.roomID,
		TurnID:       state.turnID,
		ToolCallID:   tool.callID,
		ToolName:     tool.toolName,
		ToolKind:     ToolApprovalKindMCP,
		RuleToolName: mcpToolName,
		ServerLabel:  serverLabel,
		TTL:          ttl,
	})

	// If approvals are disabled, not required, or already always-allowed, auto-approve
	// without prompting. Otherwise emit an approval request to the UI.
	needsApproval := oc.toolApprovalsRuntimeEnabled() && oc.toolApprovalsRequireForMCP() && !oc.isMcpAlwaysAllowed(serverLabel, mcpToolName)
	if needsApproval && state.heartbeat != nil {
		needsApproval = false
	}
	if needsApproval {
		if !state.uiToolApprovalRequested[approvalID] {
			state.uiToolApprovalRequested[approvalID] = true
			oc.emitUIToolApprovalRequest(ctx, portal, state, approvalID, tool.callID, tool.toolName, tool.eventID, oc.toolApprovalsTTLSeconds())
		}
	} else {
		_ = oc.resolveToolApproval(state.roomID, approvalID, ToolApprovalDecision{
			Approve:   true,
			DecidedAt: time.Now(),
			DecidedBy: oc.UserLogin.UserMXID,
		})
	}
}

func (oc *AIClient) handleResponseOutputItemAdded(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	activeTools map[string]*activeToolCall,
	item responses.ResponseOutputItemUnion,
) {
	desc := deriveToolDescriptorForOutputItem(item, state)
	if !desc.ok {
		return
	}
	if state == nil {
		return
	}
	tool := oc.upsertActiveToolFromDescriptor(ctx, portal, state, activeTools, desc)
	if tool == nil {
		return
	}
	if state.uiToolOutputFinalized[tool.callID] {
		return
	}

	if item.Type == "mcp_approval_request" {
		oc.gateMcpToolApproval(ctx, portal, state, tool, desc, item)
		return
	}

	if desc.input != nil {
		if tool.input.Len() == 0 {
			tool.input.WriteString(stringifyJSONValue(desc.input))
		}
		oc.emitUIToolInputAvailable(ctx, portal, state, tool.callID, tool.toolName, desc.input, desc.providerExecuted)
	}
}

func (oc *AIClient) handleResponseOutputItemDone(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	activeTools map[string]*activeToolCall,
	item responses.ResponseOutputItemUnion,
) {
	desc := deriveToolDescriptorForOutputItem(item, state)
	if !desc.ok {
		return
	}
	if state == nil {
		return
	}
	tool := oc.upsertActiveToolFromDescriptor(ctx, portal, state, activeTools, desc)
	if tool == nil {
		return
	}
	if state.uiToolOutputFinalized[tool.callID] {
		return
	}

	if item.Type == "mcp_approval_request" {
		oc.gateMcpToolApproval(ctx, portal, state, tool, desc, item)
		return
	}

	if desc.input != nil {
		if tool.input.Len() == 0 {
			tool.input.WriteString(stringifyJSONValue(desc.input))
		}
		oc.emitUIToolInputAvailable(ctx, portal, state, tool.callID, tool.toolName, desc.input, desc.providerExecuted)
	}

	if files := codeInterpreterFileParts(item); len(files) > 0 {
		for _, file := range files {
			recordGeneratedFile(state, file.url, file.mediaType)
			oc.emitUIFile(ctx, portal, state, file.url, file.mediaType)
		}
	}

	result := responseOutputItemResultPayload(item)
	resultStatus := ResultStatusSuccess
	statusText := strings.ToLower(strings.TrimSpace(item.Status))
	errorText := strings.TrimSpace(item.Error)
	switch {
	case outputItemLooksDenied(item):
		oc.emitUIToolOutputDenied(ctx, portal, state, tool.callID)
		resultStatus = ResultStatusDenied
	case statusText == "failed" || statusText == "incomplete" || errorText != "":
		if errorText == "" {
			errorText = fmt.Sprintf("%s failed", tool.toolName)
		}
		oc.emitUIToolOutputError(ctx, portal, state, tool.callID, errorText, true)
		resultStatus = ResultStatusError
	default:
		oc.emitUIToolOutputAvailable(ctx, portal, state, tool.callID, result, true, false)
	}

	resultJSON, _ := json.Marshal(result)
	resultEventID := oc.sendToolResultEvent(ctx, portal, state, tool, string(resultJSON), resultStatus)
	outputMap := map[string]any{}
	if converted := toJSONObject(result); len(converted) > 0 {
		outputMap = converted
	} else if result != nil {
		outputMap = map[string]any{"result": result}
	}

	state.toolCalls = append(state.toolCalls, ToolCallMetadata{
		CallID:        tool.callID,
		ToolName:      tool.toolName,
		ToolType:      string(tool.toolType),
		Input:         parseToolInputPayload(tool.input.String()),
		Output:        outputMap,
		Status:        string(ToolStatusCompleted),
		ResultStatus:  string(resultStatus),
		ErrorMessage:  errorText,
		StartedAtMs:   tool.startedAtMs,
		CompletedAtMs: time.Now().UnixMilli(),
		CallEventID:   string(tool.eventID),
		ResultEventID: string(resultEventID),
	})
}

// streamingResponseWithToolSchemaFallback retries via Chat Completions when the provider
// rejects tool schemas in the Responses API.
