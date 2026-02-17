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
	"maunium.net/go/mautrix/id"
)

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

		if state.initialEventID == "" && !state.suppressSend {
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
	oc.emitUIToolInputDelta(ctx, portal, state, tool.callID, name, delta, tool.toolType == ToolTypeProvider)
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
		oc.emitUIToolInputError(ctx, portal, state, tool.callID, toolName, argsJSON, "Invalid JSON tool input", tool.toolType == ToolTypeProvider, false)
	}
	oc.emitUIToolInputAvailable(ctx, portal, state, tool.callID, toolName, inputMap, tool.toolType == ToolTypeProvider)

	resultStatus := ResultStatusSuccess
	var result string
	if !oc.isToolEnabled(meta, toolName) {
		resultStatus = ResultStatusError
		result = fmt.Sprintf("Error: tool %s is disabled", toolName)
	} else {
		// Tool approval gating for dangerous builtin tools.
		if argsObj, ok := inputMap.(map[string]any); ok {
			required, action := oc.builtinToolApprovalRequirement(toolName, argsObj)
			if required && oc.isBuiltinAlwaysAllowed(toolName, action) {
				required = false
			}
			if required && state.heartbeat != nil {
				required = false
			}
			if required {
				approvalID := NewCallID()
				ttl := time.Duration(oc.toolApprovalsTTLSeconds()) * time.Second
				oc.registerToolApproval(struct {
					ApprovalID string
					RoomID     id.RoomID
					TurnID     string

					ToolCallID string
					ToolName   string

					ToolKind     ToolApprovalKind
					RuleToolName string
					ServerLabel  string
					Action       string

					TTL time.Duration
				}{
					ApprovalID:   approvalID,
					RoomID:       state.roomID,
					TurnID:       state.turnID,
					ToolCallID:   tool.callID,
					ToolName:     toolName,
					ToolKind:     ToolApprovalKindBuiltin,
					RuleToolName: toolName,
					Action:       action,
					TTL:          ttl,
				})
				oc.emitUIToolApprovalRequest(ctx, portal, state, approvalID, tool.callID, toolName, tool.eventID, oc.toolApprovalsTTLSeconds())
				decision, _, ok := oc.waitToolApproval(ctx, approvalID)
				if !ok {
					if oc.toolApprovalsAskFallback() == "allow" {
						decision = ToolApprovalDecision{Approve: true, Reason: "fallback"}
					} else {
						decision = ToolApprovalDecision{Approve: false, Reason: "timeout"}
					}
				}
				if !decision.Approve {
					resultStatus = ResultStatusDenied
					result = "Denied by user"
					oc.emitUIToolOutputDenied(ctx, portal, state, tool.callID)
				}
			}
		} else if approvalFallbackForNonObject {
			// If args aren't a JSON object, still gate by tool name.
			required, action := oc.builtinToolApprovalRequirement(toolName, nil)
			if required && oc.isBuiltinAlwaysAllowed(toolName, action) {
				required = false
			}
			if required && state.heartbeat != nil {
				required = false
			}
			if required {
				approvalID := NewCallID()
				ttl := time.Duration(oc.toolApprovalsTTLSeconds()) * time.Second
				oc.registerToolApproval(struct {
					ApprovalID   string
					RoomID       id.RoomID
					TurnID       string
					ToolCallID   string
					ToolName     string
					ToolKind     ToolApprovalKind
					RuleToolName string
					ServerLabel  string
					Action       string
					TTL          time.Duration
				}{
					ApprovalID:   approvalID,
					RoomID:       state.roomID,
					TurnID:       state.turnID,
					ToolCallID:   tool.callID,
					ToolName:     toolName,
					ToolKind:     ToolApprovalKindBuiltin,
					RuleToolName: toolName,
					Action:       action,
					TTL:          ttl,
				})
				oc.emitUIToolApprovalRequest(ctx, portal, state, approvalID, tool.callID, toolName, tool.eventID, oc.toolApprovalsTTLSeconds())
				decision, _, ok := oc.waitToolApproval(ctx, approvalID)
				if !ok {
					if oc.toolApprovalsAskFallback() == "allow" {
						decision = ToolApprovalDecision{Approve: true, Reason: "fallback"}
					} else {
						decision = ToolApprovalDecision{Approve: false, Reason: "timeout"}
					}
				}
				if !decision.Approve {
					resultStatus = ResultStatusDenied
					result = "Denied by user"
					oc.emitUIToolOutputDenied(ctx, portal, state, tool.callID)
				}
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

	// Check for TTS audio result (AUDIO: prefix).
	displayResult := result
	if strings.HasPrefix(result, TTSResultPrefix) {
		audioB64 := strings.TrimPrefix(result, TTSResultPrefix)
		audioData, err := base64.StdEncoding.DecodeString(audioB64)
		if err != nil {
			log.Warn().Err(err).Msg("Failed to decode TTS audio" + logSuffix)
			displayResult = "Error: failed to decode TTS audio"
			resultStatus = ResultStatusError
		} else {
			mimeType := detectAudioMime(audioData, "audio/mpeg")
			// Send audio message.
			if _, mediaURL, err := oc.sendGeneratedAudio(ctx, portal, audioData, mimeType, state.turnID); err != nil {
				log.Warn().Err(err).Msg("Failed to send TTS audio" + logSuffix)
				displayResult = "Error: failed to send TTS audio"
				resultStatus = ResultStatusError
			} else {
				recordGeneratedFile(state, mediaURL, mimeType)
				oc.emitUIFile(ctx, portal, state, mediaURL, mimeType)
				displayResult = "Audio message sent successfully"
			}
		}
		result = displayResult
	}

	// Extract image generation prompt for use as caption on sent images.
	var imageCaption string
	if prompt, err := parseToolArgsPrompt(argsJSON); err == nil {
		imageCaption = prompt
	}

	// Check for image generation result (IMAGE: / IMAGES: prefix).
	if strings.HasPrefix(result, ImagesResultPrefix) {
		payload := strings.TrimPrefix(result, ImagesResultPrefix)
		var images []string
		if err := json.Unmarshal([]byte(payload), &images); err != nil {
			log.Warn().Err(err).Msg("Failed to parse generated images payload" + logSuffix)
			displayResult = "Error: failed to parse generated images"
			resultStatus = ResultStatusError
		} else {
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
				oc.emitUIFile(ctx, portal, state, mediaURL, mimeType)
				sentURLs = append(sentURLs, mediaURL)
				success++
			}
			if success == len(images) && success > 0 {
				displayResult = fmt.Sprintf("Images generated and sent to the user (%d). Media URLs: %s", success, strings.Join(sentURLs, ", "))
			} else if success > 0 {
				displayResult = fmt.Sprintf("Images generated with %d/%d sent successfully. Media URLs: %s", success, len(images), strings.Join(sentURLs, ", "))
				resultStatus = ResultStatusError
			} else {
				displayResult = "Error: failed to send generated images"
				resultStatus = ResultStatusError
			}
		}
		result = displayResult
	} else if strings.HasPrefix(result, ImageResultPrefix) {
		imageB64 := strings.TrimPrefix(result, ImageResultPrefix)
		imageData, mimeType, err := decodeBase64Image(imageB64)
		if err != nil {
			log.Warn().Err(err).Msg("Failed to decode generated image" + logSuffix)
			displayResult = "Error: failed to decode generated image"
			resultStatus = ResultStatusError
		} else {
			// Send image message.
			if _, mediaURL, err := oc.sendGeneratedImage(ctx, portal, imageData, mimeType, state.turnID, imageCaption); err != nil {
				log.Warn().Err(err).Msg("Failed to send generated image" + logSuffix)
				displayResult = "Error: failed to send generated image"
				resultStatus = ResultStatusError
			} else {
				recordGeneratedFile(state, mediaURL, mimeType)
				oc.emitUIFile(ctx, portal, state, mediaURL, mimeType)
				displayResult = fmt.Sprintf("Image generated and sent to the user. Media URL: %s", mediaURL)
			}
		}
		result = displayResult
	}

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
		oc.emitUIToolOutputAvailable(ctx, portal, state, tool.callID, result, tool.toolType == ToolTypeProvider, false)
	} else if resultStatus != ResultStatusDenied {
		oc.emitUIToolOutputError(ctx, portal, state, tool.callID, result, tool.toolType == ToolTypeProvider)
	}

	// Normalize input for storage.
	inputMapForMeta := map[string]any{}
	if parsed, ok := inputMap.(map[string]any); ok {
		inputMapForMeta = parsed
	} else if raw, ok := inputMap.(string); ok && raw != "" {
		inputMapForMeta = map[string]any{"_raw": raw}
	}

	// Track tool call in metadata.
	completedAt := time.Now().UnixMilli()
	resultEventID := oc.sendToolResultEvent(ctx, portal, state, tool, result, resultStatus)
	state.toolCalls = append(state.toolCalls, ToolCallMetadata{
		CallID:        tool.callID,
		ToolName:      toolName,
		ToolType:      string(tool.toolType),
		Input:         inputMapForMeta,
		Output:        map[string]any{"result": result},
		Status:        string(ToolStatusCompleted),
		ResultStatus:  string(resultStatus),
		StartedAtMs:   tool.startedAtMs,
		CompletedAtMs: completedAt,
		CallEventID:   string(tool.eventID),
		ResultEventID: string(resultEventID),
	})
}
