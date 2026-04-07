package ai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
)

// processToolMediaResult handles TTS audio (AUDIO: prefix), single image (IMAGE: prefix),
// and multi-image (IMAGES: prefix) tool results. Returns the display-friendly result string
// and (possibly updated) result status.
func (oc *AIClient) processToolMediaResult(
	ctx context.Context,
	log zerolog.Logger,
	portal *bridgev2.Portal,
	state *streamingState,
	argsJSON string,
	result string,
	resultStatus ResultStatus,
	logSuffix string,
) (string, ResultStatus) {
	// TTS audio (AUDIO: prefix)
	if audioB64, ok := strings.CutPrefix(result, TTSResultPrefix); ok {
		audioData, err := base64.StdEncoding.DecodeString(audioB64)
		if err != nil {
			log.Warn().Err(err).Msg("Failed to decode TTS audio" + logSuffix)
			return "Error: failed to decode TTS audio", ResultStatusError
		}
		mimeType := detectAudioMime(audioData, "audio/mpeg")
		if _, mediaURL, err := oc.sendGeneratedAudio(ctx, portal, audioData, mimeType, state.turn.ID()); err != nil {
			log.Warn().Err(err).Msg("Failed to send TTS audio" + logSuffix)
			return "Error: failed to send TTS audio", ResultStatusError
		} else {
			recordGeneratedFile(state, mediaURL, mimeType)
			state.writer().File(ctx, mediaURL, mimeType)
			return "Audio message sent successfully", resultStatus
		}
	}

	// Extract image caption from tool args.
	var imageCaption string
	if prompt, err := parseToolArgsPrompt(argsJSON); err == nil {
		imageCaption = prompt
	}

	// Multiple images (IMAGES: prefix)
	if payload, ok := strings.CutPrefix(result, ImagesResultPrefix); ok {
		var images []string
		if err := json.Unmarshal([]byte(payload), &images); err != nil {
			log.Warn().Err(err).Msg("Failed to parse generated images payload" + logSuffix)
			return "Error: failed to parse generated images", ResultStatusError
		}
		success := 0
		var sentURLs []string
		for _, imageB64 := range images {
			imageData, mimeType, err := decodeBase64Image(imageB64)
			if err != nil {
				log.Warn().Err(err).Msg("Failed to decode generated image" + logSuffix)
				continue
			}
			_, mediaURL, err := oc.sendGeneratedImage(ctx, portal, imageData, mimeType, state.turn.ID(), imageCaption)
			if err != nil {
				log.Warn().Err(err).Msg("Failed to send generated image" + logSuffix)
				continue
			}
			recordGeneratedFile(state, mediaURL, mimeType)
			state.writer().File(ctx, mediaURL, mimeType)
			sentURLs = append(sentURLs, mediaURL)
			success++
		}
		if success == len(images) && success > 0 {
			return fmt.Sprintf("Images generated and sent to the user (%d). Media URLs: %s", success, strings.Join(sentURLs, ", ")), resultStatus
		} else if success > 0 {
			return fmt.Sprintf("Images generated with %d/%d sent successfully. Media URLs: %s", success, len(images), strings.Join(sentURLs, ", ")), ResultStatusError
		}
		return "Error: failed to send generated images", ResultStatusError
	}

	// Single image (IMAGE: prefix)
	if imageB64, ok := strings.CutPrefix(result, ImageResultPrefix); ok {
		imageData, mimeType, err := decodeBase64Image(imageB64)
		if err != nil {
			log.Warn().Err(err).Msg("Failed to decode generated image" + logSuffix)
			return "Error: failed to decode generated image", ResultStatusError
		}
		if _, mediaURL, err := oc.sendGeneratedImage(ctx, portal, imageData, mimeType, state.turn.ID(), imageCaption); err != nil {
			log.Warn().Err(err).Msg("Failed to send generated image" + logSuffix)
			return "Error: failed to send generated image", ResultStatusError
		} else {
			recordGeneratedFile(state, mediaURL, mimeType)
			state.writer().File(ctx, mediaURL, mimeType)
			return fmt.Sprintf("Image generated and sent to the user. Media URL: %s", mediaURL), resultStatus
		}
	}

	return result, resultStatus
}

// ensureActiveToolCall returns the existing activeToolCall for itemID, or creates and
// registers a new one with the given toolType. This is the shared constructor used by
// both function-call and provider/MCP tool handlers.
func (oc *AIClient) ensureActiveToolCall(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	meta *PortalMetadata,
	activeTools *streamToolRegistry,
	key string,
	name string,
	toolType ToolType,
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
	lifecycle := oc.toolLifecycle(portal, state)
	tool := oc.ensureActiveToolCall(ctx, portal, state, meta, activeTools, streamToolItemKey(itemID), name, ToolTypeFunction, "")
	if tool == nil {
		return
	}
	activeTools.BindAlias(streamToolItemKey(itemID), tool)
	tool.itemID = itemID
	lifecycle.appendInputDelta(ctx, tool, name, delta, tool.toolType == ToolTypeProvider)
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
	approvalFallbackForNonObject bool,
	logSuffix string,
) {
	tool := oc.ensureActiveToolCall(ctx, portal, state, meta, activeTools, streamToolItemKey(itemID), name, ToolTypeFunction, arguments)
	if tool == nil {
		return
	}
	activeTools.BindAlias(streamToolItemKey(itemID), tool)
	tool.itemID = itemID
	execution := oc.executeStreamingBuiltinTool(ctx, log, portal, state, meta, tool, name, arguments, approvalFallbackForNonObject, logSuffix)
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
	approvalFallbackForNonObject bool,
	logSuffix string,
) streamingBuiltinToolExecution {
	lifecycle := oc.toolLifecycle(portal, state)
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
		state.writer().Tools().InputError(ctx, tool.callID, toolName, argsJSON, "Invalid JSON tool input", tool.toolType == ToolTypeProvider)
	}
	lifecycle.emitInput(ctx, tool, toolName, inputMap, tool.toolType == ToolTypeProvider)

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
		} else if approvalFallbackForNonObject && oc.isBuiltinToolDenied(ctx, portal, state, tool, toolName, nil) {
			resultStatus = ResultStatusDenied
			result = "Denied by user"
		}
		if resultStatus != ResultStatusDenied {
			touchAgentLoopActivity(ctx)
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
			touchAgentLoopActivity(ctx)
		}
	}

	result, resultStatus = oc.processToolMediaResult(ctx, log, portal, state, argsJSON, result, resultStatus, logSuffix)
	if resultStatus == ResultStatusSuccess {
		collectToolOutputCitations(state, toolName, result)
	}
	lifecycle.completeResult(
		ctx,
		tool,
		tool.toolType == ToolTypeProvider,
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
