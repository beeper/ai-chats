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

	airuntime "github.com/beeper/agentremote/pkg/runtime"
	"github.com/beeper/agentremote/sdk"
)

func stableMCPApprovalID(toolCallID string, desc responseToolDescriptor) string {
	input := stringifyJSONValue(desc.input)
	sum := sha256.Sum256([]byte(strings.TrimSpace(toolCallID) + "\n" + desc.toolName + "\n" + input))
	return "mcp_approval_" + hex.EncodeToString(sum[:8])
}

func (oc *AIClient) startStreamingMCPApproval(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	params ToolApprovalParams,
	needsPrompt bool,
) (sdk.ApprovalHandle, error) {
	handle, created := oc.startTurnApproval(ctx, portal, state, state.turn, params, needsPrompt)
	if !created {
		return nil, fmt.Errorf("failed to register MCP approval request")
	}
	if needsPrompt {
		return handle, nil
	}
	if err := oc.resolveToolApproval(params.ApprovalID, true, sdk.ApprovalReasonAutoApproved); err != nil {
		return nil, fmt.Errorf("failed to auto-approve MCP tool call: %w", err)
	}
	return handle, nil
}

func (oc *AIClient) upsertActiveToolFromDescriptor(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	activeTools *streamToolRegistry,
	desc responseToolDescriptor,
) (*activeToolCall, bool) {
	if activeTools == nil || strings.TrimSpace(desc.callID) == "" {
		return nil, false
	}
	lifecycle := oc.toolLifecycle(portal, state)
	tool, created := activeTools.Upsert(desc.registryKey, func(canonicalKey string) *activeToolCall {
		return &activeToolCall{
			callID:      SanitizeToolCallID(desc.callID, "strict"),
			toolName:    desc.toolName,
			toolType:    desc.toolType,
			startedAtMs: time.Now().UnixMilli(),
			itemID:      desc.itemID,
		}
	})
	if created && strings.TrimSpace(desc.itemID) == "" {
		zerolog.Ctx(ctx).Warn().Str("registry_key", desc.registryKey).Msg("active tool created without item id")
	}
	if tool == nil {
		return nil, false
	}
	if strings.TrimSpace(desc.callID) != "" {
		tool.callID = SanitizeToolCallID(desc.callID, "strict")
	}
	if strings.TrimSpace(desc.approvalID) != "" {
		tool.approvalID = strings.TrimSpace(desc.approvalID)
	}
	if strings.TrimSpace(desc.itemID) != "" {
		tool.itemID = desc.itemID
		activeTools.BindAlias(streamToolItemKey(desc.itemID), tool)
	}
	activeTools.BindAlias(streamToolCallKey(tool.callID), tool)
	if tool.approvalID != "" {
		activeTools.BindAlias(streamToolApprovalKey(tool.approvalID), tool)
	}
	if strings.TrimSpace(desc.toolName) != "" {
		tool.toolName = desc.toolName
	}
	if desc.toolType != "" {
		tool.toolType = desc.toolType
	}
	if uiState := currentStreamingUIState(state); uiState != nil {
		uiState.UIToolNameByToolCallID[tool.callID] = tool.toolName
		uiState.UIToolTypeByToolCallID[tool.callID] = tool.toolType
	}

	if created {
		lifecycle.ensureInputStart(ctx, tool, desc.providerExecuted, nil)
	}
	return tool, created
}

func (oc *AIClient) ensureActiveToolForStreamItem(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	activeTools *streamToolRegistry,
	itemID string,
	item responses.ResponseOutputItemUnion,
) *activeToolCall {
	if activeTools == nil || state == nil {
		return nil
	}
	if tool := activeTools.Lookup(streamToolItemKey(itemID)); tool != nil {
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
	activeTools *streamToolRegistry,
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
	activeTools *streamToolRegistry,
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
	activeTools *streamToolRegistry,
	itemID string,
	item responses.ResponseOutputItemUnion,
) {
	lifecycle := oc.toolLifecycle(portal, state)
	tool := oc.ensureActiveToolForStreamItem(ctx, portal, state, activeTools, itemID, item)
	if tool == nil {
		return
	}
	if uiState := currentStreamingUIState(state); uiState != nil && uiState.UIToolOutputFinalized[tool.callID] {
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
	lifecycle.fail(ctx, tool, true, resultStatus, errorText, nil)
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
	tool.approvalID = approvalID
	if uiState := currentStreamingUIState(state); uiState != nil {
		uiState.UIToolCallIDByApproval[approvalID] = tool.callID
	}
	oc.toolLifecycle(portal, state).emitInput(ctx, tool, tool.toolName, desc.input, true)
	state.pendingMcpApprovalsSeen[approvalID] = true
	parsed := item.AsMcpApprovalRequest()
	serverLabel := strings.TrimSpace(parsed.ServerLabel)
	mcpToolName := strings.TrimSpace(parsed.Name)
	presentation := buildMCPApprovalPresentation(serverLabel, mcpToolName, desc.input)
	ttl := time.Duration(oc.toolApprovalsTTLSeconds()) * time.Second
	params := ToolApprovalParams{
		ApprovalID:   approvalID,
		RoomID:       state.roomID,
		TurnID:       state.turn.ID(),
		ToolCallID:   tool.callID,
		ToolName:     tool.toolName,
		ToolKind:     ToolApprovalKindMCP,
		RuleToolName: mcpToolName,
		ServerLabel:  serverLabel,
		Presentation: presentation,
		TTL:          ttl,
	}

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
	actions := streamTurnActions{oc: oc, ctx: ctx, portal: portal, state: state}
	if err := actions.approvalRequested(params, needsApproval); err != nil {
		delete(state.pendingMcpApprovalsSeen, approvalID)
		if uiState := currentStreamingUIState(state); uiState != nil {
			delete(uiState.UIToolApprovalRequested, approvalID)
		}
		oc.toolLifecycle(portal, state).fail(ctx, tool, true, ResultStatusError, err.Error(), nil)
		return
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
	activeTools *streamToolRegistry,
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
	if uiState := currentStreamingUIState(state); uiState != nil && uiState.UIToolOutputFinalized[tool.callID] {
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
	activeTools *streamToolRegistry,
	item responses.ResponseOutputItemUnion,
) {
	tool, desc, created, ok := oc.resolveOutputItemTool(ctx, portal, state, activeTools, item)
	if !ok {
		return
	}
	if created || desc.input != nil {
		oc.emitToolInputIfAvailable(ctx, portal, state, tool, desc)
	}
}

func (oc *AIClient) handleResponseOutputItemDone(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	activeTools *streamToolRegistry,
	item responses.ResponseOutputItemUnion,
) {
	tool, desc, created, ok := oc.resolveOutputItemTool(ctx, portal, state, activeTools, item)
	if !ok {
		return
	}
	if created || desc.input != nil {
		oc.emitToolInputIfAvailable(ctx, portal, state, tool, desc)
	}

	if files := codeInterpreterFileParts(item); len(files) > 0 {
		for _, file := range files {
			recordGeneratedFile(state, file.URL, file.MediaType)
			state.writer().File(ctx, file.URL, file.MediaType)
		}
	}
	actions := streamTurnActions{oc: oc, ctx: ctx, portal: portal, state: state}
	actions.toolResultCompleted(tool, item)
}

// Response stream output helpers.
