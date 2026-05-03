package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/ai-chats/pkg/matrixevents"
)

// ensureActiveToolCall returns the existing activeToolCall for itemID, or creates and
// registers a new one with the given toolType. This is the shared constructor used by
// function-call handlers.
func (oc *AIClient) ensureActiveToolCall(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	meta *PortalMetadata,
	activeTools *streamToolRegistry,
	key string,
	name string,
	toolType matrixevents.ToolType,
	initialInput string,
) *activeToolCall {
	tool, created := activeTools.Upsert(key, func(canonicalKey string) *activeToolCall {
		callID := strings.TrimSpace(strings.TrimPrefix(canonicalKey, "call:"))
		if callID == "" {
			callID = NewCallID()
		}
		tool := &activeToolCall{
			callID:      callID,
			toolName:    name,
			toolType:    toolType,
			startedAtMs: time.Now().UnixMilli(),
		}
		if strings.TrimSpace(initialInput) != "" {
			tool.input.WriteString(initialInput)
		}
		return tool
	})
	if tool == nil {
		return nil
	}
	if created && meta != nil && state != nil && !state.hasInitialMessageTarget() && !state.suppressSend {
		oc.ensureGhostDisplayName(ctx, oc.effectiveModel(meta))
	}
	return tool
}

func (oc *AIClient) handleFunctionCallArgumentsDelta(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	meta *PortalMetadata,
	activeTools *streamToolRegistry,
	itemID string,
	name string,
	delta string,
) {
	lifecycle := newToolLifecycle(state)
	tool := oc.ensureActiveToolCall(ctx, portal, state, meta, activeTools, streamToolItemKey(itemID), name, matrixevents.ToolTypeFunction, "")
	if tool == nil {
		return
	}
	activeTools.BindAlias(streamToolItemKey(itemID), tool)
	tool.itemID = itemID
	lifecycle.appendInputDelta(ctx, tool, name, delta, tool.toolType == matrixevents.ToolTypeProvider)
}

func (oc *AIClient) handleFunctionCallArgumentsDone(
	ctx context.Context,
	log zerolog.Logger,
	portal *bridgev2.Portal,
	state *streamingState,
	meta *PortalMetadata,
	activeTools *streamToolRegistry,
	itemID string,
	name string,
	arguments string,
	checkApprovalWithoutObject bool,
	logSuffix string,
) {
	tool := oc.ensureActiveToolCall(ctx, portal, state, meta, activeTools, streamToolItemKey(itemID), name, matrixevents.ToolTypeFunction, arguments)
	if tool == nil {
		return
	}
	activeTools.BindAlias(streamToolItemKey(itemID), tool)
	tool.itemID = itemID
	execution := oc.executeStreamingBuiltinTool(ctx, log, portal, state, meta, tool, name, arguments, checkApprovalWithoutObject, logSuffix)
	activeTools.BindAlias(streamToolCallKey(tool.callID), tool)

	// Store result for API continuation.
	tool.result = execution.result
	callID := strings.TrimSpace(tool.callID)
	if callID == "" {
		callID = itemID
	}
	state.pendingFunctionOutputs = append(state.pendingFunctionOutputs, functionCallOutput{
		callID:    callID,
		name:      execution.toolName,
		arguments: execution.argsJSON,
		output:    execution.result,
	})
}

type streamingBuiltinToolExecution struct {
	toolName     string
	argsJSON     string
	result       string
	resultStatus ResultStatus
}

func (oc *AIClient) executeStreamingBuiltinTool(
	ctx context.Context,
	log zerolog.Logger,
	portal *bridgev2.Portal,
	state *streamingState,
	meta *PortalMetadata,
	tool *activeToolCall,
	fallbackName string,
	fallbackArguments string,
	checkApprovalWithoutObject bool,
	logSuffix string,
) streamingBuiltinToolExecution {
	lifecycle := newToolLifecycle(state)
	toolName := strings.TrimSpace(tool.toolName)
	if toolName == "" {
		toolName = strings.TrimSpace(fallbackName)
	}
	tool.toolName = toolName
	argsJSON := strings.TrimSpace(tool.input.String())
	if argsJSON == "" {
		argsJSON = strings.TrimSpace(fallbackArguments)
	}
	argsJSON = normalizeToolArgsJSON(argsJSON)

	var inputMap any
	if err := json.Unmarshal([]byte(argsJSON), &inputMap); err != nil {
		inputMap = argsJSON
		state.writer().Tools().InputError(ctx, tool.callID, toolName, argsJSON, "Invalid JSON tool input", tool.toolType == matrixevents.ToolTypeProvider)
	}
	lifecycle.emitInput(ctx, tool, toolName, inputMap, tool.toolType == matrixevents.ToolTypeProvider)

	resultStatus := ResultStatusSuccess
	result := ""
	if !oc.isToolEnabled(meta, toolName) {
		resultStatus = ResultStatusError
		result = fmt.Sprintf("Error: tool %s is disabled", toolName)
	} else {
		if argsObj, ok := inputMap.(map[string]any); ok {
			if oc.isBuiltinToolDenied(ctx, portal, state, tool, toolName, argsObj) {
				resultStatus = ResultStatusDenied
				result = "Denied by user"
			}
		} else if checkApprovalWithoutObject && oc.isBuiltinToolDenied(ctx, portal, state, tool, toolName, nil) {
			resultStatus = ResultStatusDenied
			result = "Denied by user"
		}
		if resultStatus != ResultStatusDenied {
			touchStreamingActivity(ctx)
			toolCtx := WithBridgeToolContext(ctx, &BridgeToolContext{
				Client:        oc,
				Portal:        portal,
				Meta:          meta,
				SourceEventID: state.sourceEventID(),
				SenderID:      state.senderID(),
			})
			var err error
			result, err = oc.executeBuiltinTool(toolCtx, portal, toolName, argsJSON)
			if err != nil {
				log.Warn().Err(err).Str("tool", toolName).Msg("Tool execution failed" + logSuffix)
				result = fmt.Sprintf("Error: %s", err)
				resultStatus = ResultStatusError
			}
			touchStreamingActivity(ctx)
		}
	}

	if resultStatus == ResultStatusSuccess {
		collectToolOutputCitations(state, toolName, result)
	}
	lifecycle.completeResult(
		ctx,
		tool,
		tool.toolType == matrixevents.ToolTypeProvider,
		resultStatus,
		result,
		result,
		map[string]any{"result": result},
		parseToolInputPayload(argsJSON),
	)

	return streamingBuiltinToolExecution{
		toolName:     toolName,
		argsJSON:     argsJSON,
		result:       result,
		resultStatus: resultStatus,
	}
}

// recordToolCallResult appends a ToolCallMetadata for a tool that has already been
// finalized (success, failure, or provider-executed). Unlike recordCompletedToolCall
// it accepts pre-built output/status/error fields, covering failure and provider cases.
func recordToolCallResult(
	state *streamingState,
	tool *activeToolCall,
	status ToolStatus,
	resultStatus ResultStatus,
	errorText string,
	output map[string]any,
	input map[string]any,
) {
	state.toolCalls = append(state.toolCalls, ToolCallMetadata{
		CallID:        tool.callID,
		ToolName:      tool.toolName,
		ToolType:      string(tool.toolType),
		Input:         input,
		Output:        output,
		Status:        string(status),
		ResultStatus:  string(resultStatus),
		ErrorMessage:  errorText,
		StartedAtMs:   tool.startedAtMs,
		CompletedAtMs: time.Now().UnixMilli(),
	})
}
