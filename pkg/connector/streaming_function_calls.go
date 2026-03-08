package connector

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
	if strings.HasPrefix(result, TTSResultPrefix) {
		audioB64 := strings.TrimPrefix(result, TTSResultPrefix)
		audioData, err := base64.StdEncoding.DecodeString(audioB64)
		if err != nil {
			log.Warn().Err(err).Msg("Failed to decode TTS audio" + logSuffix)
			return "Error: failed to decode TTS audio", ResultStatusError
		}
		mimeType := detectAudioMime(audioData, "audio/mpeg")
		if _, mediaURL, err := oc.sendGeneratedAudio(ctx, portal, audioData, mimeType, state.turnID); err != nil {
			log.Warn().Err(err).Msg("Failed to send TTS audio" + logSuffix)
			return "Error: failed to send TTS audio", ResultStatusError
		} else {
			recordGeneratedFile(state, mediaURL, mimeType)
			oc.uiEmitter(state).EmitUIFile(ctx, portal, mediaURL, mimeType)
			return "Audio message sent successfully", resultStatus
		}
	}

	// Extract image caption from tool args.
	var imageCaption string
	if prompt, err := parseToolArgsPrompt(argsJSON); err == nil {
		imageCaption = prompt
	}

	// Multiple images (IMAGES: prefix)
	if strings.HasPrefix(result, ImagesResultPrefix) {
		payload := strings.TrimPrefix(result, ImagesResultPrefix)
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
			_, mediaURL, err := oc.sendGeneratedImage(ctx, portal, imageData, mimeType, state.turnID, imageCaption)
			if err != nil {
				log.Warn().Err(err).Msg("Failed to send generated image" + logSuffix)
				continue
			}
			recordGeneratedFile(state, mediaURL, mimeType)
			oc.uiEmitter(state).EmitUIFile(ctx, portal, mediaURL, mimeType)
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
	if strings.HasPrefix(result, ImageResultPrefix) {
		imageB64 := strings.TrimPrefix(result, ImageResultPrefix)
		imageData, mimeType, err := decodeBase64Image(imageB64)
		if err != nil {
			log.Warn().Err(err).Msg("Failed to decode generated image" + logSuffix)
			return "Error: failed to decode generated image", ResultStatusError
		}
		if _, mediaURL, err := oc.sendGeneratedImage(ctx, portal, imageData, mimeType, state.turnID, imageCaption); err != nil {
			log.Warn().Err(err).Msg("Failed to send generated image" + logSuffix)
			return "Error: failed to send generated image", ResultStatusError
		} else {
			recordGeneratedFile(state, mediaURL, mimeType)
			oc.uiEmitter(state).EmitUIFile(ctx, portal, mediaURL, mimeType)
			return fmt.Sprintf("Image generated and sent to the user. Media URL: %s", mediaURL), resultStatus
		}
	}

	return result, resultStatus
}

func (oc *AIClient) ensureFunctionCallTool(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	meta *PortalMetadata,
	activeTools map[string]*activeToolCall,
	itemID string,
	name string,
	initialInput string,
) *activeToolCall {
	tool, exists := activeTools[itemID]
	if !exists {
		callID := itemID
		if strings.TrimSpace(callID) == "" {
			callID = NewCallID()
		}
		tool = &activeToolCall{
			callID:      callID,
			toolName:    name,
			toolType:    ToolTypeFunction,
			startedAtMs: time.Now().UnixMilli(),
			itemID:      itemID,
		}
		if strings.TrimSpace(initialInput) != "" {
			tool.input.WriteString(initialInput)
		}
		activeTools[itemID] = tool

		if !state.hasInitialMessageTarget() && !state.suppressSend {
			oc.ensureGhostDisplayName(ctx, oc.effectiveModel(meta))
		}
		if strings.TrimSpace(tool.toolName) != "" {
			tool.eventID = oc.sendToolCallEvent(ctx, portal, state, tool)
		}
	}
	return tool
}

func (oc *AIClient) handleFunctionCallArgumentsDelta(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	meta *PortalMetadata,
	activeTools map[string]*activeToolCall,
	itemID string,
	name string,
	delta string,
) {
	tool := oc.ensureFunctionCallTool(ctx, portal, state, meta, activeTools, itemID, name, "")
	tool.itemID = itemID
	tool.input.WriteString(delta)
	oc.uiEmitter(state).EmitUIToolInputDelta(ctx, portal, tool.callID, name, delta, tool.toolType == ToolTypeProvider)
}

func (oc *AIClient) handleFunctionCallArgumentsDone(
	ctx context.Context,
	log zerolog.Logger,
	portal *bridgev2.Portal,
	state *streamingState,
	meta *PortalMetadata,
	activeTools map[string]*activeToolCall,
	itemID string,
	name string,
	arguments string,
	approvalFallbackForNonObject bool,
	logSuffix string,
) {
	tool := oc.ensureFunctionCallTool(ctx, portal, state, meta, activeTools, itemID, name, arguments)
	tool.itemID = itemID

	toolName := strings.TrimSpace(tool.toolName)
	if toolName == "" {
		toolName = strings.TrimSpace(name)
	}
	tool.toolName = toolName
	if tool.eventID == "" {
		tool.eventID = oc.sendToolCallEvent(ctx, portal, state, tool)
	}
	argsJSON := strings.TrimSpace(tool.input.String())
	if argsJSON == "" {
		argsJSON = strings.TrimSpace(arguments)
	}
	argsJSON = normalizeToolArgsJSON(argsJSON)

	var inputMap any
	if err := json.Unmarshal([]byte(argsJSON), &inputMap); err != nil {
		inputMap = argsJSON
		oc.uiEmitter(state).EmitUIToolInputError(ctx, portal, tool.callID, toolName, argsJSON, "Invalid JSON tool input", tool.toolType == ToolTypeProvider, false)
	}
	oc.uiEmitter(state).EmitUIToolInputAvailable(ctx, portal, tool.callID, toolName, inputMap, tool.toolType == ToolTypeProvider)

	resultStatus := ResultStatusSuccess
	var result string
	if !oc.isToolEnabled(meta, toolName) {
		resultStatus = ResultStatusError
		result = fmt.Sprintf("Error: tool %s is disabled", toolName)
	} else {
		// Tool approval gating for dangerous builtin tools.
		if argsObj, ok := inputMap.(map[string]any); ok {
			if oc.isBuiltinToolDenied(ctx, portal, state, tool, toolName, argsObj) {
				resultStatus = ResultStatusDenied
				result = "Denied by user"
			}
		} else if approvalFallbackForNonObject {
			if oc.isBuiltinToolDenied(ctx, portal, state, tool, toolName, nil) {
				resultStatus = ResultStatusDenied
				result = "Denied by user"
			}
		}

		// If denied, skip tool execution but still send a tool result to the model.
		if resultStatus != ResultStatusDenied {
			// Wrap context with bridge info for tools that need it (e.g., channel-edit, react).
			toolCtx := WithBridgeToolContext(ctx, &BridgeToolContext{
				Client:        oc,
				Portal:        portal,
				Meta:          meta,
				SourceEventID: state.sourceEventID,
				SenderID:      state.senderID,
			})
			var err error
			result, err = oc.executeBuiltinTool(toolCtx, portal, toolName, argsJSON)
			if err != nil {
				log.Warn().Err(err).Str("tool", toolName).Msg("Tool execution failed" + logSuffix)
				result = fmt.Sprintf("Error: %s", err.Error())
				resultStatus = ResultStatusError
			}
		}
	}

	result, resultStatus = oc.processToolMediaResult(ctx, log, portal, state, argsJSON, result, resultStatus, logSuffix)

	// Store result for API continuation.
	tool.result = result
	collectToolOutputCitations(state, toolName, result)
	state.pendingFunctionOutputs = append(state.pendingFunctionOutputs, functionCallOutput{
		callID:    itemID,
		name:      toolName,
		arguments: argsJSON,
		output:    result,
	})

	// Emit UI tool output immediately so desktop sees completion without waiting for timeline event send.
	if resultStatus == ResultStatusSuccess {
		oc.uiEmitter(state).EmitUIToolOutputAvailable(ctx, portal, tool.callID, result, tool.toolType == ToolTypeProvider, false)
	} else if resultStatus != ResultStatusDenied {
		oc.uiEmitter(state).EmitUIToolOutputError(ctx, portal, tool.callID, result, tool.toolType == ToolTypeProvider)
	}

	recordCompletedToolCall(ctx, oc, portal, state, tool, toolName, argsJSON, result, resultStatus)
}

func recordCompletedToolCall(
	ctx context.Context,
	oc *AIClient,
	portal *bridgev2.Portal,
	state *streamingState,
	tool *activeToolCall,
	toolName string,
	argsJSON string,
	result string,
	resultStatus ResultStatus,
) {
	completedAt := time.Now().UnixMilli()
	resultEventID := oc.sendToolResultEvent(ctx, portal, state, tool, result, resultStatus)
	state.toolCalls = append(state.toolCalls, ToolCallMetadata{
		CallID:        tool.callID,
		ToolName:      toolName,
		ToolType:      string(tool.toolType),
		Input:         parseToolInputPayload(argsJSON),
		Output:        map[string]any{"result": result},
		Status:        string(ToolStatusCompleted),
		ResultStatus:  string(resultStatus),
		StartedAtMs:   tool.startedAtMs,
		CompletedAtMs: completedAt,
		CallEventID:   string(tool.eventID),
		ResultEventID: string(resultEventID),
	})
}
