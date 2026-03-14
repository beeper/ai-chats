package ai

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/openai/openai-go/v3/responses"
	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote"
	airuntime "github.com/beeper/agentremote/pkg/runtime"
)

func stableMCPApprovalID(toolCallID string, desc responseToolDescriptor) string {
	input := stringifyJSONValue(desc.input)
	sum := sha256.Sum256([]byte(strings.TrimSpace(toolCallID) + "\n" + desc.toolName + "\n" + input))
	return "mcp_approval_" + hex.EncodeToString(sum[:8])
}

func (oc *AIClient) upsertActiveToolFromDescriptor(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	activeTools map[string]*activeToolCall,
	desc responseToolDescriptor,
) (*activeToolCall, bool) {
	if activeTools == nil || strings.TrimSpace(desc.itemID) == "" || strings.TrimSpace(desc.callID) == "" {
		return nil, false
	}
	lifecycle := oc.toolLifecycle(portal, state)
	tool, ok := activeTools[desc.itemID]
	created := !ok || tool == nil
	if ok && tool == nil {
		// A nil map entry is unexpected here; recreate it so streaming can continue.
		zerolog.Ctx(ctx).Warn().Str("item_id", desc.itemID).Msg("active tool map contained nil entry")
	}
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
	state.ui.UIToolNameByToolCallID[tool.callID] = tool.toolName
	state.ui.UIToolTypeByToolCallID[tool.callID] = tool.toolType

	if created {
		lifecycle.ensureInputStart(ctx, tool, desc.providerExecuted, nil)
	}
	return tool, created
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
	tool, _ := oc.upsertActiveToolFromDescriptor(ctx, portal, state, activeTools, itemDesc)
	return tool
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
	lifecycle := oc.toolLifecycle(portal, state)
	tool := oc.ensureActiveToolForStreamItem(ctx, portal, state, activeTools, itemID, item)
	if tool == nil {
		return
	}
	lifecycle.appendInputDelta(ctx, tool, tool.toolName, delta, tool.toolType == ToolTypeProvider)
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
	lifecycle := oc.toolLifecycle(portal, state)
	tool := oc.ensureActiveToolForStreamItem(ctx, portal, state, activeTools, itemID, item)
	if tool == nil {
		return
	}
	if tool.input.Len() == 0 && strings.TrimSpace(inputText) != "" {
		tool.input.WriteString(inputText)
	}
	lifecycle.emitInput(ctx, tool, tool.toolName, parseJSONOrRaw(tool.input.String()), tool.toolType == ToolTypeProvider)
}

func (oc *AIClient) handleMCPCallFailedFromOutputItem(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	activeTools map[string]*activeToolCall,
	itemID string,
	item responses.ResponseOutputItemUnion,
) {
	lifecycle := oc.toolLifecycle(portal, state)
	tool := oc.ensureActiveToolForStreamItem(ctx, portal, state, activeTools, itemID, item)
	if tool == nil {
		return
	}
	if state != nil && state.ui.UIToolOutputFinalized[tool.callID] {
		return
	}
	errorText := strings.TrimSpace(item.Error)
	if errorText == "" {
		errorText = "MCP tool call failed"
	}
	denied := outputItemLooksDenied(item)
	resultStatus := ResultStatusError
	if denied {
		resultStatus = ResultStatusDenied
	}
	lifecycle.finalize(ctx, tool, toolFinalizeOptions{
		providerExecuted: true,
		status:           ToolStatusFailed,
		resultStatus:     resultStatus,
		errorText:        errorText,
		input:            nil,
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
	if state == nil || tool == nil {
		return
	}
	approvalID := strings.TrimSpace(item.ID)
	if approvalID == "" {
		approvalID = stableMCPApprovalID(tool.callID, desc)
	}
	if state.pendingMcpApprovalsSeen[approvalID] {
		return
	}
	if tool.input.Len() == 0 {
		tool.input.WriteString(stringifyJSONValue(desc.input))
	}
	state.ui.UIToolCallIDByApproval[approvalID] = tool.callID
	oc.toolLifecycle(portal, state).emitInput(ctx, tool, tool.toolName, desc.input, true)
	state.pendingMcpApprovalsSeen[approvalID] = true
	parsed := item.AsMcpApprovalRequest()
	serverLabel := strings.TrimSpace(parsed.ServerLabel)
	mcpToolName := strings.TrimSpace(parsed.Name)
	presentation := buildMCPApprovalPresentation(serverLabel, mcpToolName, desc.input)
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
		Presentation: presentation,
		TTL:          ttl,
	})

	// If approvals are disabled, not required, or already always-allowed, auto-approve
	// without prompting. Otherwise emit an approval request to the UI.
	runtimeDecision := airuntime.DecideToolApproval(airuntime.ToolPolicyInput{
		ToolName:      mcpToolName,
		ToolKind:      "mcp",
		CallID:        tool.callID,
		RequireForMCP: oc.toolApprovalsRequireForMCP(),
	})
	needsApproval := oc.toolApprovalsRuntimeEnabled() && runtimeDecision.State == airuntime.ToolApprovalRequired && !oc.isMcpAlwaysAllowed(serverLabel, mcpToolName)
	if needsApproval && state.heartbeat != nil {
		needsApproval = false
	}
	if needsApproval {
		if !state.ui.UIToolApprovalRequested[approvalID] {
			state.ui.UIToolApprovalRequested[approvalID] = true
			if !oc.emitUIToolApprovalRequest(ctx, portal, state, approvalID, tool.callID, tool.toolName, presentation, "", oc.toolApprovalsTTLSeconds()) {
				if err := oc.approvalFlow.Resolve(approvalID, agentremote.ApprovalDecisionPayload{
					ApprovalID: approvalID,
					Reason:     agentremote.ApprovalReasonDeliveryError,
				}); err != nil {
					delete(state.pendingMcpApprovalsSeen, approvalID)
					oc.toolLifecycle(portal, state).finalize(ctx, tool, toolFinalizeOptions{
						providerExecuted: true,
						status:           ToolStatusFailed,
						resultStatus:     ResultStatusError,
						errorText:        "failed to deliver MCP approval prompt",
					})
					oc.loggerForContext(ctx).Warn().Err(err).Str("approval_id", approvalID).Msg("Failed to resolve undeliverable MCP approval prompt")
				}
			}
		}
	} else {
		if err := oc.approvalFlow.Resolve(approvalID, agentremote.ApprovalDecisionPayload{
			ApprovalID: approvalID,
			Approved:   true,
			Reason:     "auto_approved",
		}); err != nil {
			delete(state.pendingMcpApprovalsSeen, approvalID)
			oc.toolLifecycle(portal, state).finalize(ctx, tool, toolFinalizeOptions{
				providerExecuted: true,
				status:           ToolStatusFailed,
				resultStatus:     ResultStatusError,
				errorText:        "failed to auto-approve MCP tool call",
			})
			oc.loggerForContext(ctx).Warn().Err(err).Str("approval_id", approvalID).Msg("Failed to auto-approve MCP tool call")
		}
	}
}

// resolveOutputItemTool performs the common setup shared by handleResponseOutputItemAdded
// and handleResponseOutputItemDone: derives the tool descriptor, upserts the active tool,
// checks finalization, and handles mcp_approval_request gating.
// Returns (tool, desc, ok). When ok is false the caller should return early.
func (oc *AIClient) resolveOutputItemTool(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	activeTools map[string]*activeToolCall,
	item responses.ResponseOutputItemUnion,
) (*activeToolCall, responseToolDescriptor, bool, bool) {
	desc := deriveToolDescriptorForOutputItem(item, state)
	if !desc.ok || state == nil {
		return nil, desc, false, false
	}
	tool, created := oc.upsertActiveToolFromDescriptor(ctx, portal, state, activeTools, desc)
	if tool == nil {
		return nil, desc, false, false
	}
	if state.ui.UIToolOutputFinalized[tool.callID] {
		return nil, desc, false, false
	}
	if item.Type == "mcp_approval_request" {
		oc.gateMcpToolApproval(ctx, portal, state, tool, desc, item)
		return nil, desc, false, false
	}
	return tool, desc, created, true
}

// emitToolInputIfAvailable records the tool's input text and emits a UI input-available
// event when the descriptor carries a non-nil input payload.
func (oc *AIClient) emitToolInputIfAvailable(ctx context.Context, portal *bridgev2.Portal, state *streamingState, tool *activeToolCall, desc responseToolDescriptor) {
	if desc.input == nil {
		return
	}
	if tool.input.Len() == 0 {
		tool.input.WriteString(stringifyJSONValue(desc.input))
	}
	oc.toolLifecycle(portal, state).emitInput(ctx, tool, tool.toolName, desc.input, desc.providerExecuted)
}

func (oc *AIClient) handleResponseOutputItemAdded(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	activeTools map[string]*activeToolCall,
	item responses.ResponseOutputItemUnion,
) {
	tool, desc, created, ok := oc.resolveOutputItemTool(ctx, portal, state, activeTools, item)
	if !ok {
		return
	}
	if created {
		oc.emitToolInputIfAvailable(ctx, portal, state, tool, desc)
	}
}

func (oc *AIClient) handleResponseOutputItemDone(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	activeTools map[string]*activeToolCall,
	item responses.ResponseOutputItemUnion,
) {
	tool, desc, created, ok := oc.resolveOutputItemTool(ctx, portal, state, activeTools, item)
	if !ok {
		return
	}
	if created {
		oc.emitToolInputIfAvailable(ctx, portal, state, tool, desc)
	}

	if files := codeInterpreterFileParts(item); len(files) > 0 {
		for _, file := range files {
			recordGeneratedFile(state, file.URL, file.MediaType)
			oc.writer(state, portal).File(ctx, file.URL, file.MediaType)
		}
	}

	result := responseOutputItemResultPayload(item)
	resultStatus := ResultStatusSuccess
	toolStatus := ToolStatusCompleted
	statusText := strings.ToLower(strings.TrimSpace(item.Status))
	errorText := strings.TrimSpace(item.Error)
	switch {
	case outputItemLooksDenied(item):
		resultStatus = ResultStatusDenied
		toolStatus = ToolStatusFailed
	case statusText == "failed" || statusText == "incomplete" || errorText != "":
		if errorText == "" {
			errorText = fmt.Sprintf("%s failed", tool.toolName)
		}
		resultStatus = ResultStatusError
		toolStatus = ToolStatusFailed
	}
	oc.toolLifecycle(portal, state).finalize(ctx, tool, toolFinalizeOptions{
		providerExecuted: true,
		status:           toolStatus,
		resultStatus:     resultStatus,
		errorText:        errorText,
		output:           result,
		input:            parseToolInputPayload(tool.input.String()),
	})
}

// Response stream output helpers.
