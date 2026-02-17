package connector

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// streamingResponse handles streaming using the Responses API
// This is the preferred streaming method as it supports reasoning tokens
// Returns (success, contextLengthError)
func (oc *AIClient) streamingResponse(
	ctx context.Context,
	evt *event.Event,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	messages []openai.ChatCompletionMessageParamUnion,
) (bool, *ContextLengthError, error) {
	portalID := ""
	if portal != nil {
		portalID = string(portal.ID)
	}
	log := zerolog.Ctx(ctx).With().
		Str("portal_id", portalID).
		Logger()
	// Tool loops can legitimately require several rounds (e.g. multi-step file ops).
	// Keep a cap to prevent runaway loops, but 3 rounds is too low in practice.
	maxToolRounds := 10

	// Initialize streaming state with turn tracking
	// Pass source event ID for [[reply_to_current]] directive support
	var sourceEventID id.EventID
	senderID := ""
	if evt != nil {
		sourceEventID = evt.ID
		if evt.Sender != "" {
			senderID = evt.Sender.String()
		}
	}
	roomID := id.RoomID("")
	if portal != nil {
		roomID = portal.MXID
	}
	state := newStreamingState(ctx, meta, sourceEventID, senderID, roomID)
	state.replyTarget = oc.resolveInitialReplyTarget(evt)
	if state.roomID != "" {
		oc.markRoomRunStreaming(state.roomID, true)
		defer oc.markRoomRunStreaming(state.roomID, false)
	}

	// Ensure model ghost is in the room before any operations
	if !state.suppressSend {
		if err := oc.ensureModelInRoom(ctx, portal); err != nil {
			log.Warn().Err(err).Msg("Failed to ensure model is in room")
			// Continue anyway - typing will fail gracefully
		}
	}

	// Create typing controller with TTL and automatic refresh
	var typingCtrl *TypingController
	var typingSignals *TypingSignaler
	touchTyping := func() {}
	isHeartbeat := state.heartbeat != nil
	if !state.suppressSend && !isHeartbeat {
		mode := oc.resolveTypingMode(meta, typingContextFromContext(ctx), isHeartbeat)
		interval := oc.resolveTypingInterval(meta)
		if interval > 0 && mode != TypingModeNever {
			typingCtrl = NewTypingController(oc, ctx, portal, TypingControllerOptions{
				Interval: interval,
				TTL:      typingTTL,
			})
			typingSignals = NewTypingSignaler(typingCtrl, mode, isHeartbeat)
			touchTyping = func() {
				typingCtrl.RefreshTTL()
			}
		}
	}
	if typingSignals != nil {
		typingSignals.SignalRunStart()
	}
	defer func() {
		if typingCtrl != nil {
			typingCtrl.MarkRunComplete()
			typingCtrl.MarkDispatchIdle()
		}
	}()

	// Apply proactive context pruning if enabled
	messages = oc.applyProactivePruning(ctx, messages, meta)

	// Build Responses API params using shared helper
	params := oc.buildResponsesAPIParams(ctx, portal, meta, messages)

	// Inject per-room PDF engine into context for OpenRouter/Beeper providers
	if oc.isOpenRouterProvider() {
		ctx = WithPDFEngine(ctx, oc.effectivePDFEngine(meta))
	}

	stream := oc.api.Responses.NewStreaming(ctx, params)
	if stream == nil {
		initErr := errors.New("responses streaming not available")
		logResponsesFailure(log, initErr, params, meta, messages, "stream_init")
		return false, nil, &PreDeltaError{Err: initErr}
	}

	// Store base input for OpenRouter stateless continuations
	if params.Input.OfInputItemList != nil {
		state.baseInput = params.Input.OfInputItemList
	}

	// Track active tool calls
	activeTools := make(map[string]*activeToolCall)

	// Emit AI SDK UI stream start and first step
	oc.emitUIStart(ctx, portal, state, meta)
	oc.emitUIStepStart(ctx, portal, state)

	// Process stream events - no debouncing, stream every delta immediately
	for stream.Next() {
		streamEvent := stream.Current()
		if streamEvent.Type != "error" {
			oc.markMessageSendSuccess(ctx, portal, evt, state)
		}

		switch streamEvent.Type {
		case "response.created", "response.queued", "response.in_progress":
			if strings.TrimSpace(streamEvent.Response.ID) != "" {
				state.responseID = streamEvent.Response.ID
			}
			oc.emitUIRuntimeMetadata(ctx, portal, state, meta, responseMetadataDeltaFromResponse(streamEvent.Response))

		case "response.failed":
			state.finishReason = "error"
			if strings.TrimSpace(streamEvent.Response.ID) != "" {
				state.responseID = streamEvent.Response.ID
			}
			oc.emitUIRuntimeMetadata(ctx, portal, state, meta, responseMetadataDeltaFromResponse(streamEvent.Response))
			if msg := strings.TrimSpace(streamEvent.Response.Error.Message); msg != "" {
				oc.emitUIError(ctx, portal, state, msg)
			}

		case "response.incomplete":
			state.finishReason = strings.TrimSpace(string(streamEvent.Response.IncompleteDetails.Reason))
			if strings.TrimSpace(state.finishReason) == "" {
				state.finishReason = "other"
			}
			if strings.TrimSpace(streamEvent.Response.ID) != "" {
				state.responseID = streamEvent.Response.ID
			}
			oc.emitUIRuntimeMetadata(ctx, portal, state, meta, responseMetadataDeltaFromResponse(streamEvent.Response))

		case "response.output_item.added":
			oc.handleResponseOutputItemAdded(ctx, portal, state, activeTools, streamEvent.Item)

		case "response.output_item.done":
			oc.handleResponseOutputItemDone(ctx, portal, state, activeTools, streamEvent.Item)

		case "response.custom_tool_call_input.delta":
			tool, exists := activeTools[streamEvent.ItemID]
			if !exists {
				itemDesc := deriveToolDescriptorForOutputItem(streamEvent.Item, state)
				if itemDesc.ok {
					tool = oc.upsertActiveToolFromDescriptor(ctx, portal, state, activeTools, itemDesc)
				}
			}
			if tool != nil {
				tool.input.WriteString(streamEvent.Delta)
				oc.emitUIToolInputDelta(ctx, portal, state, tool.callID, tool.toolName, streamEvent.Delta, tool.toolType == ToolTypeProvider)
			}

		case "response.custom_tool_call_input.done":
			tool, exists := activeTools[streamEvent.ItemID]
			if !exists {
				itemDesc := deriveToolDescriptorForOutputItem(streamEvent.Item, state)
				if itemDesc.ok {
					tool = oc.upsertActiveToolFromDescriptor(ctx, portal, state, activeTools, itemDesc)
				}
			}
			if tool != nil {
				if tool.input.Len() == 0 && strings.TrimSpace(streamEvent.Input) != "" {
					tool.input.WriteString(streamEvent.Input)
				}
				oc.emitUIToolInputAvailable(ctx, portal, state, tool.callID, tool.toolName, parseJSONOrRaw(tool.input.String()), tool.toolType == ToolTypeProvider)
			}

		case "response.code_interpreter_call_code.delta":
			tool, exists := activeTools[streamEvent.ItemID]
			if !exists {
				itemDesc := deriveToolDescriptorForOutputItem(streamEvent.Item, state)
				if itemDesc.ok {
					tool = oc.upsertActiveToolFromDescriptor(ctx, portal, state, activeTools, itemDesc)
				}
			}
			if tool != nil {
				tool.input.WriteString(streamEvent.Delta)
				oc.emitUIToolInputDelta(ctx, portal, state, tool.callID, tool.toolName, streamEvent.Delta, true)
			}

		case "response.code_interpreter_call_code.done":
			tool, exists := activeTools[streamEvent.ItemID]
			if !exists {
				itemDesc := deriveToolDescriptorForOutputItem(streamEvent.Item, state)
				if itemDesc.ok {
					tool = oc.upsertActiveToolFromDescriptor(ctx, portal, state, activeTools, itemDesc)
				}
			}
			if tool != nil {
				if tool.input.Len() == 0 && strings.TrimSpace(streamEvent.Code) != "" {
					tool.input.WriteString(streamEvent.Code)
				}
				oc.emitUIToolInputAvailable(ctx, portal, state, tool.callID, tool.toolName, parseJSONOrRaw(tool.input.String()), true)
			}

		case "response.mcp_call_arguments.delta":
			tool, exists := activeTools[streamEvent.ItemID]
			if !exists {
				itemDesc := deriveToolDescriptorForOutputItem(streamEvent.Item, state)
				if itemDesc.ok {
					tool = oc.upsertActiveToolFromDescriptor(ctx, portal, state, activeTools, itemDesc)
				}
			}
			if tool != nil {
				tool.input.WriteString(streamEvent.Delta)
				oc.emitUIToolInputDelta(ctx, portal, state, tool.callID, tool.toolName, streamEvent.Delta, true)
			}

		case "response.mcp_call_arguments.done":
			tool, exists := activeTools[streamEvent.ItemID]
			if !exists {
				itemDesc := deriveToolDescriptorForOutputItem(streamEvent.Item, state)
				if itemDesc.ok {
					tool = oc.upsertActiveToolFromDescriptor(ctx, portal, state, activeTools, itemDesc)
				}
			}
			if tool != nil {
				if tool.input.Len() == 0 && strings.TrimSpace(streamEvent.Arguments) != "" {
					tool.input.WriteString(streamEvent.Arguments)
				}
				oc.emitUIToolInputAvailable(ctx, portal, state, tool.callID, tool.toolName, parseJSONOrRaw(tool.input.String()), true)
			}

		case "response.mcp_call.failed":
			tool, exists := activeTools[streamEvent.ItemID]
			if !exists {
				itemDesc := deriveToolDescriptorForOutputItem(streamEvent.Item, state)
				if itemDesc.ok {
					tool = oc.upsertActiveToolFromDescriptor(ctx, portal, state, activeTools, itemDesc)
				}
			}
			if tool != nil {
				if state != nil && state.uiToolOutputFinalized[tool.callID] {
					break
				}
				errorText := strings.TrimSpace(streamEvent.Item.Error)
				if errorText == "" {
					errorText = "MCP tool call failed"
				}
				denied := outputItemLooksDenied(streamEvent.Item)
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

		case "response.output_text.delta":
			touchTyping()
			delta := maybePrependTextSeparator(state, streamEvent.Delta)
			state.accumulated.WriteString(delta)
			parsed := (*streamingDirectiveResult)(nil)
			if state.replyAccumulator != nil {
				parsed = state.replyAccumulator.Consume(delta, false)
			}
			if parsed != nil {
				oc.applyStreamingReplyTarget(state, parsed)
				cleaned := parsed.Text
				if typingSignals != nil {
					typingSignals.SignalTextDelta(cleaned)
				}
				if cleaned != "" {
					state.visibleAccumulated.WriteString(cleaned)
					// First token - send initial message synchronously to capture event_id
					if state.firstToken && state.visibleAccumulated.Len() > 0 {
						state.firstToken = false
						state.firstTokenAtMs = time.Now().UnixMilli()
						if !state.suppressSend && !isHeartbeat {
							// Ensure ghost display name is set before sending the first message
							oc.ensureGhostDisplayName(ctx, oc.effectiveModel(meta))
							state.initialEventID = oc.sendInitialStreamMessage(ctx, portal, state.visibleAccumulated.String(), state.turnID, state.replyTarget)
							if state.initialEventID == "" {
								errText := "failed to send initial streaming message"
								log.Error().Msg("Failed to send initial streaming message")
								state.finishReason = "error"
								oc.emitUIError(ctx, portal, state, errText)
								oc.emitUIFinish(ctx, portal, state, meta)
								return false, nil, &PreDeltaError{Err: errors.New(errText)}
							}
						}
					}
					oc.emitUITextDelta(ctx, portal, state, cleaned)
				}
			}

		case "response.reasoning_text.delta":
			touchTyping()
			if typingSignals != nil {
				typingSignals.SignalReasoningDelta()
			}
			state.reasoning.WriteString(streamEvent.Delta)

			// Check if this is first content (reasoning before text)
			if state.firstToken && state.reasoning.Len() > 0 {
				state.firstToken = false
				state.firstTokenAtMs = time.Now().UnixMilli()
				if !state.suppressSend && !isHeartbeat {
					oc.ensureGhostDisplayName(ctx, oc.effectiveModel(meta))
					// Send empty initial message - will be replaced with content later
					state.initialEventID = oc.sendInitialStreamMessage(ctx, portal, "...", state.turnID, state.replyTarget)
					if state.initialEventID == "" {
						errText := "failed to send initial streaming message"
						log.Error().Msg("Failed to send initial streaming message")
						state.finishReason = "error"
						oc.emitUIError(ctx, portal, state, errText)
						oc.emitUIFinish(ctx, portal, state, meta)
						return false, nil, &PreDeltaError{Err: errors.New(errText)}
					}
				}
			}

			oc.emitUIReasoningDelta(ctx, portal, state, streamEvent.Delta)

		case "response.reasoning_summary_text.delta":
			if strings.TrimSpace(streamEvent.Delta) != "" {
				state.reasoning.WriteString(streamEvent.Delta)
				oc.emitUIReasoningDelta(ctx, portal, state, streamEvent.Delta)
			}

		case "response.reasoning_text.done", "response.reasoning_summary_text.done":
			if strings.TrimSpace(streamEvent.Text) != "" {
				state.reasoning.WriteString(streamEvent.Text)
				oc.emitUIReasoningDelta(ctx, portal, state, streamEvent.Text)
			}

		case "response.refusal.delta":
			touchTyping()
			if typingSignals != nil {
				typingSignals.SignalTextDelta(streamEvent.Delta)
			}
			oc.emitUITextDelta(ctx, portal, state, streamEvent.Delta)

		case "response.refusal.done":
			if strings.TrimSpace(streamEvent.Refusal) != "" {
				oc.emitUITextDelta(ctx, portal, state, streamEvent.Refusal)
			}

		case "response.output_text.done":
			// text-end is emitted from emitUIFinish to keep one contiguous part.

		case "response.function_call_arguments.delta":
			touchTyping()
			if typingSignals != nil {
				typingSignals.SignalToolStart()
			}
			// Get or create active tool call
			tool, exists := activeTools[streamEvent.ItemID]
			if !exists {
				callID := streamEvent.ItemID
				if strings.TrimSpace(callID) == "" {
					callID = NewCallID()
				}
				tool = &activeToolCall{
					callID:      callID,
					toolName:    streamEvent.Name,
					toolType:    ToolTypeFunction,
					startedAtMs: time.Now().UnixMilli(),
					itemID:      streamEvent.ItemID,
				}
				activeTools[streamEvent.ItemID] = tool

				if state.initialEventID == "" && !state.suppressSend {
					oc.ensureGhostDisplayName(ctx, oc.effectiveModel(meta))
				}
				if strings.TrimSpace(tool.toolName) != "" {
					tool.eventID = oc.sendToolCallEvent(ctx, portal, state, tool)
				}
			}

			// Accumulate arguments
			tool.input.WriteString(streamEvent.Delta)

			oc.emitUIToolInputDelta(ctx, portal, state, tool.callID, streamEvent.Name, streamEvent.Delta, tool.toolType == ToolTypeProvider)

		case "response.function_call_arguments.done":
			touchTyping()
			if typingSignals != nil {
				typingSignals.SignalToolStart()
			}
			// Function call complete - execute the tool and send result
			tool, exists := activeTools[streamEvent.ItemID]
			if !exists {
				// Create tool if we missed the delta events
				callID := streamEvent.ItemID
				if strings.TrimSpace(callID) == "" {
					callID = NewCallID()
				}
				tool = &activeToolCall{
					callID:      callID,
					toolName:    streamEvent.Name,
					toolType:    ToolTypeFunction,
					startedAtMs: time.Now().UnixMilli(),
				}
				tool.input.WriteString(streamEvent.Arguments)
				activeTools[streamEvent.ItemID] = tool
			}

			// Store the item ID for continuation (this is the call_id for the Responses API)
			tool.itemID = streamEvent.ItemID

			toolName := strings.TrimSpace(tool.toolName)
			if toolName == "" {
				toolName = strings.TrimSpace(streamEvent.Name)
			}
			tool.toolName = toolName
			if tool.eventID == "" {
				tool.eventID = oc.sendToolCallEvent(ctx, portal, state, tool)
			}
			argsJSON := strings.TrimSpace(tool.input.String())
			if argsJSON == "" {
				argsJSON = strings.TrimSpace(streamEvent.Arguments)
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
				} else {
					// If we couldn't parse args as JSON object, still gate by tool name.
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
					// Wrap context with bridge info for tools that need it (e.g., channel-edit, react)
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
						log.Warn().Err(err).Str("tool", toolName).Msg("Tool execution failed")
						result = fmt.Sprintf("Error: %s", err.Error())
						resultStatus = ResultStatusError
					}
				}
			}

			// Check for TTS audio result (AUDIO: prefix)
			displayResult := result
			if strings.HasPrefix(result, TTSResultPrefix) {
				audioB64 := strings.TrimPrefix(result, TTSResultPrefix)
				audioData, err := base64.StdEncoding.DecodeString(audioB64)
				if err != nil {
					log.Warn().Err(err).Msg("Failed to decode TTS audio")
					displayResult = "Error: failed to decode TTS audio"
					resultStatus = ResultStatusError
				} else {
					mimeType := detectAudioMime(audioData, "audio/mpeg")
					// Send audio message
					if _, mediaURL, err := oc.sendGeneratedAudio(ctx, portal, audioData, mimeType, state.turnID); err != nil {
						log.Warn().Err(err).Msg("Failed to send TTS audio")
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

			// Check for image generation result (IMAGE: / IMAGES: prefix)
			if strings.HasPrefix(result, ImagesResultPrefix) {
				payload := strings.TrimPrefix(result, ImagesResultPrefix)
				var images []string
				if err := json.Unmarshal([]byte(payload), &images); err != nil {
					log.Warn().Err(err).Msg("Failed to parse generated images payload")
					displayResult = "Error: failed to parse generated images"
					resultStatus = ResultStatusError
				} else {
					success := 0
					var sentURLs []string
					for _, imageB64 := range images {
						imageData, mimeType, err := decodeBase64Image(imageB64)
						if err != nil {
							log.Warn().Err(err).Msg("Failed to decode generated image")
							continue
						}
						_, mediaURL, err := oc.sendGeneratedImage(ctx, portal, imageData, mimeType, state.turnID, imageCaption)
						if err != nil {
							log.Warn().Err(err).Msg("Failed to send generated image")
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
					log.Warn().Err(err).Msg("Failed to decode generated image")
					displayResult = "Error: failed to decode generated image"
					resultStatus = ResultStatusError
				} else {
					// Send image message
					if _, mediaURL, err := oc.sendGeneratedImage(ctx, portal, imageData, mimeType, state.turnID, imageCaption); err != nil {
						log.Warn().Err(err).Msg("Failed to send generated image")
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

			// Store result for API continuation
			tool.result = result
			collectToolOutputCitations(state, toolName, result)
			args := argsJSON
			state.pendingFunctionOutputs = append(state.pendingFunctionOutputs, functionCallOutput{
				callID:    streamEvent.ItemID,
				name:      toolName,
				arguments: args,
				output:    result,
			})

			// Emit UI tool output immediately so the desktop sees the tool
			// as completed without waiting for the timeline event send.
			if resultStatus == ResultStatusSuccess {
				oc.emitUIToolOutputAvailable(ctx, portal, state, tool.callID, result, tool.toolType == ToolTypeProvider, false)
			} else if resultStatus != ResultStatusDenied {
				oc.emitUIToolOutputError(ctx, portal, state, tool.callID, result, tool.toolType == ToolTypeProvider)
			}

			// Normalize input for storage
			inputMapForMeta := map[string]any{}
			if parsed, ok := inputMap.(map[string]any); ok {
				inputMapForMeta = parsed
			} else if raw, ok := inputMap.(string); ok && raw != "" {
				inputMapForMeta = map[string]any{"_raw": raw}
			}

			// Track tool call in metadata (sendToolResultEvent is a blocking
			// Matrix API call, but the UI update was already emitted above).
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

		case "response.file_search_call.searching", "response.file_search_call.in_progress":
			callID := strings.TrimSpace(streamEvent.ItemID)
			if callID == "" {
				callID = NewCallID()
			}
			tool, exists := activeTools[streamEvent.ItemID]
			if !exists {
				tool = &activeToolCall{
					callID:      callID,
					toolName:    "file_search",
					toolType:    ToolTypeProvider,
					startedAtMs: time.Now().UnixMilli(),
					itemID:      streamEvent.ItemID,
				}
				activeTools[streamEvent.ItemID] = tool
				tool.eventID = oc.sendToolCallEvent(ctx, portal, state, tool)
			}
			oc.emitUIToolInputDelta(ctx, portal, state, tool.callID, tool.toolName, "", true)

		case "response.file_search_call.completed":
			tool, exists := activeTools[streamEvent.ItemID]
			callID := strings.TrimSpace(streamEvent.ItemID)
			if callID == "" {
				callID = NewCallID()
			}
			if exists && tool != nil {
				callID = tool.callID
			}
			if state != nil && state.uiToolOutputFinalized[callID] {
				break
			}
			if !exists {
				tool = &activeToolCall{
					callID:      callID,
					toolName:    "file_search",
					toolType:    ToolTypeProvider,
					startedAtMs: time.Now().UnixMilli(),
					itemID:      streamEvent.ItemID,
				}
				activeTools[streamEvent.ItemID] = tool
				tool.eventID = oc.sendToolCallEvent(ctx, portal, state, tool)
			}
			output := map[string]any{"status": "completed"}
			oc.emitUIToolOutputAvailable(ctx, portal, state, callID, output, true, false)

			resultJSON, _ := json.Marshal(output)
			resultEventID := oc.sendToolResultEvent(ctx, portal, state, tool, string(resultJSON), ResultStatusSuccess)
			state.toolCalls = append(state.toolCalls, ToolCallMetadata{
				CallID:        callID,
				ToolName:      "file_search",
				ToolType:      string(tool.toolType),
				Output:        output,
				Status:        string(ToolStatusCompleted),
				ResultStatus:  string(ResultStatusSuccess),
				StartedAtMs:   tool.startedAtMs,
				CompletedAtMs: time.Now().UnixMilli(),
				CallEventID:   string(tool.eventID),
				ResultEventID: string(resultEventID),
			})

		case "response.code_interpreter_call.in_progress", "response.code_interpreter_call.interpreting":
			callID := strings.TrimSpace(streamEvent.ItemID)
			if callID == "" {
				callID = NewCallID()
			}
			tool, exists := activeTools[streamEvent.ItemID]
			if !exists {
				tool = &activeToolCall{
					callID:      callID,
					toolName:    "code_interpreter",
					toolType:    ToolTypeProvider,
					startedAtMs: time.Now().UnixMilli(),
					itemID:      streamEvent.ItemID,
				}
				activeTools[streamEvent.ItemID] = tool
				tool.eventID = oc.sendToolCallEvent(ctx, portal, state, tool)
			}
			oc.emitUIToolInputDelta(ctx, portal, state, tool.callID, tool.toolName, "", true)

		case "response.code_interpreter_call.completed":
			tool, exists := activeTools[streamEvent.ItemID]
			callID := strings.TrimSpace(streamEvent.ItemID)
			if callID == "" {
				callID = NewCallID()
			}
			if exists && tool != nil {
				callID = tool.callID
			}
			if state != nil && state.uiToolOutputFinalized[callID] {
				break
			}
			if !exists {
				tool = &activeToolCall{
					callID:      callID,
					toolName:    "code_interpreter",
					toolType:    ToolTypeProvider,
					startedAtMs: time.Now().UnixMilli(),
					itemID:      streamEvent.ItemID,
				}
				activeTools[streamEvent.ItemID] = tool
				tool.eventID = oc.sendToolCallEvent(ctx, portal, state, tool)
			}
			output := map[string]any{"status": "completed"}
			oc.emitUIToolOutputAvailable(ctx, portal, state, callID, output, true, false)

			resultJSON, _ := json.Marshal(output)
			resultEventID := oc.sendToolResultEvent(ctx, portal, state, tool, string(resultJSON), ResultStatusSuccess)
			state.toolCalls = append(state.toolCalls, ToolCallMetadata{
				CallID:        callID,
				ToolName:      "code_interpreter",
				ToolType:      string(tool.toolType),
				Output:        output,
				Status:        string(ToolStatusCompleted),
				ResultStatus:  string(ResultStatusSuccess),
				StartedAtMs:   tool.startedAtMs,
				CompletedAtMs: time.Now().UnixMilli(),
				CallEventID:   string(tool.eventID),
				ResultEventID: string(resultEventID),
			})

		case "response.mcp_list_tools.in_progress":
			callID := strings.TrimSpace(streamEvent.ItemID)
			if callID == "" {
				callID = NewCallID()
			}
			tool, exists := activeTools[streamEvent.ItemID]
			if !exists {
				tool = &activeToolCall{
					callID:      callID,
					toolName:    "mcp.list_tools",
					toolType:    ToolTypeMCP,
					startedAtMs: time.Now().UnixMilli(),
					itemID:      streamEvent.ItemID,
				}
				activeTools[streamEvent.ItemID] = tool
				tool.eventID = oc.sendToolCallEvent(ctx, portal, state, tool)
			}
			oc.emitUIToolInputDelta(ctx, portal, state, tool.callID, tool.toolName, "", true)

		case "response.mcp_list_tools.completed":
			tool, exists := activeTools[streamEvent.ItemID]
			callID := strings.TrimSpace(streamEvent.ItemID)
			if callID == "" {
				callID = NewCallID()
			}
			if exists && tool != nil {
				callID = tool.callID
			}
			if state != nil && state.uiToolOutputFinalized[callID] {
				break
			}
			if !exists {
				tool = &activeToolCall{
					callID:      callID,
					toolName:    "mcp.list_tools",
					toolType:    ToolTypeMCP,
					startedAtMs: time.Now().UnixMilli(),
					itemID:      streamEvent.ItemID,
				}
				activeTools[streamEvent.ItemID] = tool
				tool.eventID = oc.sendToolCallEvent(ctx, portal, state, tool)
			}
			output := map[string]any{"status": "completed"}
			oc.emitUIToolOutputAvailable(ctx, portal, state, callID, output, true, false)

			resultJSON, _ := json.Marshal(output)
			resultEventID := oc.sendToolResultEvent(ctx, portal, state, tool, string(resultJSON), ResultStatusSuccess)
			state.toolCalls = append(state.toolCalls, ToolCallMetadata{
				CallID:        callID,
				ToolName:      "mcp.list_tools",
				ToolType:      string(tool.toolType),
				Output:        output,
				Status:        string(ToolStatusCompleted),
				ResultStatus:  string(ResultStatusSuccess),
				StartedAtMs:   tool.startedAtMs,
				CompletedAtMs: time.Now().UnixMilli(),
				CallEventID:   string(tool.eventID),
				ResultEventID: string(resultEventID),
			})

		case "response.mcp_list_tools.failed":
			tool, exists := activeTools[streamEvent.ItemID]
			callID := strings.TrimSpace(streamEvent.ItemID)
			if callID == "" {
				callID = NewCallID()
			}
			if exists && tool != nil {
				callID = tool.callID
			}
			if state != nil && state.uiToolOutputFinalized[callID] {
				break
			}
			if !exists {
				tool = &activeToolCall{
					callID:      callID,
					toolName:    "mcp.list_tools",
					toolType:    ToolTypeMCP,
					startedAtMs: time.Now().UnixMilli(),
					itemID:      streamEvent.ItemID,
				}
				activeTools[streamEvent.ItemID] = tool
				tool.eventID = oc.sendToolCallEvent(ctx, portal, state, tool)
			}
			errText := "MCP list tools failed"
			oc.emitUIToolOutputError(ctx, portal, state, callID, errText, true)

			resultEventID := oc.sendToolResultEvent(ctx, portal, state, tool, errText, ResultStatusError)
			state.toolCalls = append(state.toolCalls, ToolCallMetadata{
				CallID:        callID,
				ToolName:      "mcp.list_tools",
				ToolType:      string(tool.toolType),
				Output:        map[string]any{"error": errText},
				Status:        string(ToolStatusFailed),
				ResultStatus:  string(ResultStatusError),
				ErrorMessage:  errText,
				StartedAtMs:   tool.startedAtMs,
				CompletedAtMs: time.Now().UnixMilli(),
				CallEventID:   string(tool.eventID),
				ResultEventID: string(resultEventID),
			})

		case "response.mcp_call.in_progress":
			callID := strings.TrimSpace(streamEvent.ItemID)
			if callID == "" {
				callID = NewCallID()
			}
			tool, exists := activeTools[streamEvent.ItemID]
			if !exists {
				tool = &activeToolCall{
					callID:      callID,
					toolName:    "mcp.call",
					toolType:    ToolTypeMCP,
					startedAtMs: time.Now().UnixMilli(),
					itemID:      streamEvent.ItemID,
				}
				activeTools[streamEvent.ItemID] = tool
				tool.eventID = oc.sendToolCallEvent(ctx, portal, state, tool)
			}
			oc.emitUIToolInputDelta(ctx, portal, state, tool.callID, tool.toolName, "", true)

		case "response.mcp_call.completed":
			tool, exists := activeTools[streamEvent.ItemID]
			callID := strings.TrimSpace(streamEvent.ItemID)
			if callID == "" {
				callID = NewCallID()
			}
			if exists && tool != nil {
				callID = tool.callID
			}
			if state != nil && state.uiToolOutputFinalized[callID] {
				break
			}
			if !exists {
				tool = &activeToolCall{
					callID:      callID,
					toolName:    "mcp.call",
					toolType:    ToolTypeMCP,
					startedAtMs: time.Now().UnixMilli(),
					itemID:      streamEvent.ItemID,
				}
				activeTools[streamEvent.ItemID] = tool
				tool.eventID = oc.sendToolCallEvent(ctx, portal, state, tool)
			}
			output := map[string]any{"status": "completed"}
			oc.emitUIToolOutputAvailable(ctx, portal, state, callID, output, true, false)

			resultJSON, _ := json.Marshal(output)
			resultEventID := oc.sendToolResultEvent(ctx, portal, state, tool, string(resultJSON), ResultStatusSuccess)
			state.toolCalls = append(state.toolCalls, ToolCallMetadata{
				CallID:        callID,
				ToolName:      "mcp.call",
				ToolType:      string(tool.toolType),
				Output:        output,
				Status:        string(ToolStatusCompleted),
				ResultStatus:  string(ResultStatusSuccess),
				StartedAtMs:   tool.startedAtMs,
				CompletedAtMs: time.Now().UnixMilli(),
				CallEventID:   string(tool.eventID),
				ResultEventID: string(resultEventID),
			})

		case "response.web_search_call.searching", "response.web_search_call.in_progress":
			touchTyping()
			if typingSignals != nil {
				typingSignals.SignalToolStart()
			}
			// Web search starting
			callID := streamEvent.ItemID
			if strings.TrimSpace(callID) == "" {
				callID = NewCallID()
			}
			tool := &activeToolCall{
				callID:      callID,
				toolName:    "web_search",
				toolType:    ToolTypeProvider,
				startedAtMs: time.Now().UnixMilli(),
				itemID:      streamEvent.ItemID,
			}
			tool.eventID = oc.sendToolCallEvent(ctx, portal, state, tool)
			activeTools[streamEvent.ItemID] = tool

			if state.initialEventID == "" && !state.suppressSend {
				oc.ensureGhostDisplayName(ctx, oc.effectiveModel(meta))
			}
			oc.emitUIToolInputDelta(ctx, portal, state, tool.callID, "web_search", "", true)

		case "response.web_search_call.completed":
			touchTyping()
			// Web search completed
			tool, exists := activeTools[streamEvent.ItemID]
			callID := ""
			if exists && tool != nil {
				callID = tool.callID
			}
			if callID == "" {
				callID = streamEvent.ItemID
			}
			if exists {
				// Track tool call
				output := map[string]any{"status": "completed"}
				resultJSON, _ := json.Marshal(output)
				resultEventID := oc.sendToolResultEvent(ctx, portal, state, tool, string(resultJSON), ResultStatusSuccess)
				state.toolCalls = append(state.toolCalls, ToolCallMetadata{
					CallID:        callID,
					ToolName:      "web_search",
					ToolType:      string(tool.toolType),
					Output:        output,
					Status:        string(ToolStatusCompleted),
					ResultStatus:  string(ResultStatusSuccess),
					StartedAtMs:   tool.startedAtMs,
					CompletedAtMs: time.Now().UnixMilli(),
					CallEventID:   string(tool.eventID),
					ResultEventID: string(resultEventID),
				})
			}
			oc.emitUIToolOutputAvailable(ctx, portal, state, callID, map[string]any{"status": "completed"}, true, false)

		case "response.image_generation_call.in_progress", "response.image_generation_call.generating":
			touchTyping()
			if typingSignals != nil {
				typingSignals.SignalToolStart()
			}
			// Image generation in progress
			tool, exists := activeTools[streamEvent.ItemID]
			if !exists {
				callID := streamEvent.ItemID
				if strings.TrimSpace(callID) == "" {
					callID = NewCallID()
				}
				tool = &activeToolCall{
					callID:      callID,
					toolName:    "image_generation",
					toolType:    ToolTypeProvider,
					startedAtMs: time.Now().UnixMilli(),
					itemID:      streamEvent.ItemID,
				}
				tool.eventID = oc.sendToolCallEvent(ctx, portal, state, tool)
				activeTools[streamEvent.ItemID] = tool

				if state.initialEventID == "" && !state.suppressSend {
					oc.ensureGhostDisplayName(ctx, oc.effectiveModel(meta))
				}
			}
			oc.emitUIToolInputDelta(ctx, portal, state, tool.callID, "image_generation", "", true)

			log.Debug().Str("item_id", streamEvent.ItemID).Msg("Image generation in progress")

		case "response.image_generation_call.completed":
			touchTyping()
			if typingSignals != nil {
				typingSignals.SignalToolStart()
			}
			// Image generation completed - the actual image data will be in response.completed
			tool, exists := activeTools[streamEvent.ItemID]
			callID := ""
			if exists && tool != nil {
				callID = tool.callID
			}
			if callID == "" {
				callID = streamEvent.ItemID
			}
			if exists {
				// Track tool call
				output := map[string]any{"status": "completed"}
				resultJSON, _ := json.Marshal(output)
				resultEventID := oc.sendToolResultEvent(ctx, portal, state, tool, string(resultJSON), ResultStatusSuccess)
				state.toolCalls = append(state.toolCalls, ToolCallMetadata{
					CallID:        callID,
					ToolName:      "image_generation",
					ToolType:      string(tool.toolType),
					Output:        output,
					Status:        string(ToolStatusCompleted),
					ResultStatus:  string(ResultStatusSuccess),
					StartedAtMs:   tool.startedAtMs,
					CompletedAtMs: time.Now().UnixMilli(),
					CallEventID:   string(tool.eventID),
					ResultEventID: string(resultEventID),
				})
			}
			log.Info().Str("item_id", streamEvent.ItemID).Msg("Image generation completed")
			oc.emitUIToolOutputAvailable(ctx, portal, state, callID, map[string]any{"status": "completed"}, true, false)

		case "response.image_generation_call.partial_image":
			touchTyping()
			if typingSignals != nil {
				typingSignals.SignalToolStart()
			}
			oc.emitStreamEvent(ctx, portal, state, map[string]any{
				"type":      "data-image_generation_partial",
				"data":      map[string]any{"item_id": streamEvent.ItemID, "index": streamEvent.PartialImageIndex, "image_b64": streamEvent.PartialImageB64},
				"transient": true,
			})

		case "response.output_text.annotation.added":
			if citation, ok := extractURLCitation(streamEvent.Annotation); ok {
				state.sourceCitations = mergeSourceCitations(state.sourceCitations, []sourceCitation{citation})
				oc.emitUISourceURL(ctx, portal, state, citation)
			}
			if document, ok := extractDocumentCitation(streamEvent.Annotation); ok {
				state.sourceDocuments = append(state.sourceDocuments, document)
				oc.emitUISourceDocument(ctx, portal, state, document)
			}
			oc.emitStreamEvent(ctx, portal, state, map[string]any{
				"type":      "data-annotation",
				"data":      map[string]any{"annotation": streamEvent.Annotation, "index": streamEvent.AnnotationIndex},
				"transient": true,
			})

		case "response.completed":
			state.completedAtMs = time.Now().UnixMilli()

			if streamEvent.Response.Usage.TotalTokens > 0 || streamEvent.Response.Usage.InputTokens > 0 || streamEvent.Response.Usage.OutputTokens > 0 {
				state.promptTokens = streamEvent.Response.Usage.InputTokens
				state.completionTokens = streamEvent.Response.Usage.OutputTokens
				state.reasoningTokens = streamEvent.Response.Usage.OutputTokensDetails.ReasoningTokens
				state.totalTokens = streamEvent.Response.Usage.TotalTokens
			}

			if streamEvent.Response.Status == "completed" {
				state.finishReason = "stop"
			} else {
				state.finishReason = string(streamEvent.Response.Status)
			}
			// Capture response ID for persistence (will save to DB and portal after streaming completes)
			if streamEvent.Response.ID != "" {
				state.responseID = streamEvent.Response.ID
			}
			oc.emitUIMessageMetadata(ctx, portal, state, oc.buildUIMessageMetadata(state, meta, true))

			// Extract any generated images from response output
			for _, output := range streamEvent.Response.Output {
				if output.Type == "image_generation_call" {
					imgOutput := output.AsImageGenerationCall()
					if imgOutput.Status == "completed" && imgOutput.Result != "" {
						state.pendingImages = append(state.pendingImages, generatedImage{
							itemID:   imgOutput.ID,
							imageB64: imgOutput.Result,
							turnID:   state.turnID,
						})
						log.Debug().Str("item_id", imgOutput.ID).Msg("Captured generated image from response")
					}
				}
			}

			log.Debug().Str("reason", state.finishReason).Str("response_id", state.responseID).Int("images", len(state.pendingImages)).Msg("Response stream completed")

		case "error":
			apiErr := fmt.Errorf("API error: %s", streamEvent.Message)
			state.finishReason = "error"
			oc.emitUIError(ctx, portal, state, streamEvent.Message)
			oc.emitUIFinish(ctx, portal, state, meta)
			logResponsesFailure(log, apiErr, params, meta, messages, "stream_event_error")
			// Check for context length error
			if strings.Contains(streamEvent.Message, "context_length") || strings.Contains(streamEvent.Message, "token") {
				return false, &ContextLengthError{
					OriginalError: fmt.Errorf("%s", streamEvent.Message),
				}, nil
			}
			if state.initialEventID != "" {
				return false, nil, &NonFallbackError{Err: apiErr}
			}
			return false, nil, &PreDeltaError{Err: apiErr}
		default:
			// Ignore unknown events
		}
	}

	oc.emitUIStepFinish(ctx, portal, state)

	// Check for stream errors
	if err := stream.Err(); err != nil {
		logResponsesFailure(log, err, params, meta, messages, "stream_err")
		if errors.Is(err, context.Canceled) {
			state.finishReason = "cancelled"
			// Flush partial content if we already sent some deltas
			if state.initialEventID != "" && state.accumulated.Len() > 0 {
				oc.flushPartialStreamingMessage(context.Background(), portal, state, meta)
			}
			oc.emitUIAbort(ctx, portal, state, "cancelled")
			oc.emitUIFinish(ctx, portal, state, meta)
			if state.initialEventID != "" {
				return false, nil, &NonFallbackError{Err: err}
			}
			return false, nil, &PreDeltaError{Err: err}
		}
		cle := ParseContextLengthError(err)
		if cle != nil {
			return false, cle, nil
		}
		state.finishReason = "error"
		oc.emitUIError(ctx, portal, state, err.Error())
		oc.emitUIFinish(ctx, portal, state, meta)
		if state.initialEventID != "" {
			return false, nil, &NonFallbackError{Err: err}
		}
		return false, nil, &PreDeltaError{Err: err}
	}

	// If there are pending tool outputs or MCP approvals, send them back to the API for continuation.
	// This loop continues until the model generates a response without additional tool actions.
	continuationRound := 0
	for (len(state.pendingFunctionOutputs) > 0 || len(state.pendingMcpApprovals) > 0) && state.responseID != "" {
		// Check for context cancellation before starting a new continuation round
		if ctx.Err() != nil {
			state.finishReason = "cancelled"
			if state.initialEventID != "" && state.accumulated.Len() > 0 {
				oc.flushPartialStreamingMessage(context.Background(), portal, state, meta)
			}
			oc.emitUIAbort(ctx, portal, state, "cancelled")
			oc.emitUIFinish(ctx, portal, state, meta)
			if state.initialEventID != "" {
				return false, nil, &NonFallbackError{Err: ctx.Err()}
			}
			return false, nil, &PreDeltaError{Err: ctx.Err()}
		}

		continuationRound++
		if continuationRound > maxToolRounds {
			err := fmt.Errorf("max responses tool call rounds reached (%d)", maxToolRounds)
			log.Warn().Err(err).Int("pending_outputs", len(state.pendingFunctionOutputs)).Msg("Stopping responses continuation loop")
			state.finishReason = "error"
			oc.emitUIError(ctx, portal, state, err.Error())
			oc.emitUIFinish(ctx, portal, state, meta)
			if state.initialEventID != "" {
				return false, nil, &NonFallbackError{Err: err}
			}
			return false, nil, &PreDeltaError{Err: err}
		}
		log.Debug().
			Int("pending_outputs", len(state.pendingFunctionOutputs)).
			Int("pending_approvals", len(state.pendingMcpApprovals)).
			Str("previous_response_id", state.responseID).
			Msg("Continuing response with pending tool actions")

		pendingOutputs := append([]functionCallOutput(nil), state.pendingFunctionOutputs...)
		pendingApprovals := append([]mcpApprovalRequest(nil), state.pendingMcpApprovals...)

		approvalInputs := make([]responses.ResponseInputItemUnionParam, 0, len(pendingApprovals))
		for _, approval := range pendingApprovals {
			decision, _, ok := oc.waitToolApproval(ctx, approval.approvalID)
			if !ok {
				if oc.toolApprovalsAskFallback() == "allow" {
					decision = ToolApprovalDecision{Approve: true, Reason: "fallback"}
				} else {
					decision = ToolApprovalDecision{Approve: false, Reason: "timeout"}
				}
			}
			item := responses.ResponseInputItemParamOfMcpApprovalResponse(approval.approvalID, decision.Approve)
			if decision.Reason != "" && item.OfMcpApprovalResponse != nil {
				item.OfMcpApprovalResponse.Reason = param.NewOpt(decision.Reason)
			}
			approvalInputs = append(approvalInputs, item)

			if !decision.Approve {
				// Optimistically mark as denied in the UI; the provider may emit a denial later as well.
				oc.emitUIToolOutputDenied(ctx, portal, state, approval.toolCallID)
			}
		}

		// Build continuation request with tool outputs + approval responses
		continuationParams := oc.buildContinuationParams(ctx, state, meta, pendingOutputs, approvalInputs)

		// OpenRouter Responses API is stateless; persist tool calls in base input.
		if oc.isOpenRouterProvider() && len(state.baseInput) > 0 {
			for _, output := range pendingOutputs {
				if output.name != "" {
					args := output.arguments
					if strings.TrimSpace(args) == "" {
						args = "{}"
					}
					state.baseInput = append(state.baseInput, responses.ResponseInputItemParamOfFunctionCall(args, output.callID, output.name))
				}
				state.baseInput = append(state.baseInput, buildFunctionCallOutputItem(output.callID, output.output, true))
			}
			for _, approval := range approvalInputs {
				state.baseInput = append(state.baseInput, approval)
			}
		}

		// Reset active tools for new iteration
		activeTools = make(map[string]*activeToolCall)

		// Start continuation stream
		// Ensure the next assistant text delta can't get glued to the previous text.
		state.needsTextSeparator = true
		stream = oc.api.Responses.NewStreaming(ctx, continuationParams)
		if stream == nil {
			initErr := errors.New("continuation streaming not available")
			logResponsesFailure(log, initErr, continuationParams, meta, messages, "continuation_init")
			state.finishReason = "error"
			oc.emitUIError(ctx, portal, state, initErr.Error())
			oc.emitUIFinish(ctx, portal, state, meta)
			if state.initialEventID != "" {
				return false, nil, &NonFallbackError{Err: initErr}
			}
			return false, nil, &PreDeltaError{Err: initErr}
		}
		// Clear pending inputs only once continuation stream has actually started.
		state.pendingFunctionOutputs = nil
		state.pendingMcpApprovals = nil
		oc.emitUIStepStart(ctx, portal, state)

		// Process continuation stream events
		for stream.Next() {
			streamEvent := stream.Current()

			switch streamEvent.Type {
			case "response.created", "response.queued", "response.in_progress":
				if strings.TrimSpace(streamEvent.Response.ID) != "" {
					state.responseID = streamEvent.Response.ID
				}
				oc.emitUIRuntimeMetadata(ctx, portal, state, meta, responseMetadataDeltaFromResponse(streamEvent.Response))

			case "response.failed":
				state.finishReason = "error"
				if strings.TrimSpace(streamEvent.Response.ID) != "" {
					state.responseID = streamEvent.Response.ID
				}
				oc.emitUIRuntimeMetadata(ctx, portal, state, meta, responseMetadataDeltaFromResponse(streamEvent.Response))
				if msg := strings.TrimSpace(streamEvent.Response.Error.Message); msg != "" {
					oc.emitUIError(ctx, portal, state, msg)
				}

			case "response.incomplete":
				state.finishReason = strings.TrimSpace(string(streamEvent.Response.IncompleteDetails.Reason))
				if strings.TrimSpace(state.finishReason) == "" {
					state.finishReason = "other"
				}
				if strings.TrimSpace(streamEvent.Response.ID) != "" {
					state.responseID = streamEvent.Response.ID
				}
				oc.emitUIRuntimeMetadata(ctx, portal, state, meta, responseMetadataDeltaFromResponse(streamEvent.Response))

			case "response.output_item.added":
				oc.handleResponseOutputItemAdded(ctx, portal, state, activeTools, streamEvent.Item)

			case "response.output_item.done":
				oc.handleResponseOutputItemDone(ctx, portal, state, activeTools, streamEvent.Item)

			case "response.custom_tool_call_input.delta":
				tool, exists := activeTools[streamEvent.ItemID]
				if !exists {
					itemDesc := deriveToolDescriptorForOutputItem(streamEvent.Item, state)
					if itemDesc.ok {
						tool = oc.upsertActiveToolFromDescriptor(ctx, portal, state, activeTools, itemDesc)
					}
				}
				if tool != nil {
					tool.input.WriteString(streamEvent.Delta)
					oc.emitUIToolInputDelta(ctx, portal, state, tool.callID, tool.toolName, streamEvent.Delta, tool.toolType == ToolTypeProvider)
				}

			case "response.custom_tool_call_input.done":
				tool, exists := activeTools[streamEvent.ItemID]
				if !exists {
					itemDesc := deriveToolDescriptorForOutputItem(streamEvent.Item, state)
					if itemDesc.ok {
						tool = oc.upsertActiveToolFromDescriptor(ctx, portal, state, activeTools, itemDesc)
					}
				}
				if tool != nil {
					if tool.input.Len() == 0 && strings.TrimSpace(streamEvent.Input) != "" {
						tool.input.WriteString(streamEvent.Input)
					}
					oc.emitUIToolInputAvailable(ctx, portal, state, tool.callID, tool.toolName, parseJSONOrRaw(tool.input.String()), tool.toolType == ToolTypeProvider)
				}

			case "response.code_interpreter_call_code.delta":
				tool, exists := activeTools[streamEvent.ItemID]
				if !exists {
					itemDesc := deriveToolDescriptorForOutputItem(streamEvent.Item, state)
					if itemDesc.ok {
						tool = oc.upsertActiveToolFromDescriptor(ctx, portal, state, activeTools, itemDesc)
					}
				}
				if tool != nil {
					tool.input.WriteString(streamEvent.Delta)
					oc.emitUIToolInputDelta(ctx, portal, state, tool.callID, tool.toolName, streamEvent.Delta, true)
				}

			case "response.code_interpreter_call_code.done":
				tool, exists := activeTools[streamEvent.ItemID]
				if !exists {
					itemDesc := deriveToolDescriptorForOutputItem(streamEvent.Item, state)
					if itemDesc.ok {
						tool = oc.upsertActiveToolFromDescriptor(ctx, portal, state, activeTools, itemDesc)
					}
				}
				if tool != nil {
					if tool.input.Len() == 0 && strings.TrimSpace(streamEvent.Code) != "" {
						tool.input.WriteString(streamEvent.Code)
					}
					oc.emitUIToolInputAvailable(ctx, portal, state, tool.callID, tool.toolName, parseJSONOrRaw(tool.input.String()), true)
				}

			case "response.mcp_call_arguments.delta":
				tool, exists := activeTools[streamEvent.ItemID]
				if !exists {
					itemDesc := deriveToolDescriptorForOutputItem(streamEvent.Item, state)
					if itemDesc.ok {
						tool = oc.upsertActiveToolFromDescriptor(ctx, portal, state, activeTools, itemDesc)
					}
				}
				if tool != nil {
					tool.input.WriteString(streamEvent.Delta)
					oc.emitUIToolInputDelta(ctx, portal, state, tool.callID, tool.toolName, streamEvent.Delta, true)
				}

			case "response.mcp_call_arguments.done":
				tool, exists := activeTools[streamEvent.ItemID]
				if !exists {
					itemDesc := deriveToolDescriptorForOutputItem(streamEvent.Item, state)
					if itemDesc.ok {
						tool = oc.upsertActiveToolFromDescriptor(ctx, portal, state, activeTools, itemDesc)
					}
				}
				if tool != nil {
					if tool.input.Len() == 0 && strings.TrimSpace(streamEvent.Arguments) != "" {
						tool.input.WriteString(streamEvent.Arguments)
					}
					oc.emitUIToolInputAvailable(ctx, portal, state, tool.callID, tool.toolName, parseJSONOrRaw(tool.input.String()), true)
				}

			case "response.mcp_call.failed":
				tool, exists := activeTools[streamEvent.ItemID]
				if !exists {
					itemDesc := deriveToolDescriptorForOutputItem(streamEvent.Item, state)
					if itemDesc.ok {
						tool = oc.upsertActiveToolFromDescriptor(ctx, portal, state, activeTools, itemDesc)
					}
				}
				if tool != nil {
					if state != nil && state.uiToolOutputFinalized[tool.callID] {
						break
					}
					errorText := strings.TrimSpace(streamEvent.Item.Error)
					if errorText == "" {
						errorText = "MCP tool call failed"
					}
					denied := outputItemLooksDenied(streamEvent.Item)
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

			case "response.output_text.delta":
				touchTyping()
				delta := maybePrependTextSeparator(state, streamEvent.Delta)
				state.accumulated.WriteString(delta)
				parsed := (*streamingDirectiveResult)(nil)
				if state.replyAccumulator != nil {
					parsed = state.replyAccumulator.Consume(delta, false)
				}
				if parsed != nil {
					oc.applyStreamingReplyTarget(state, parsed)
					cleaned := parsed.Text
					if typingSignals != nil {
						typingSignals.SignalTextDelta(cleaned)
					}
					if cleaned != "" {
						state.visibleAccumulated.WriteString(cleaned)
						if state.firstToken && state.visibleAccumulated.Len() > 0 {
							state.firstToken = false
							state.firstTokenAtMs = time.Now().UnixMilli()
							if !state.suppressSend && !isHeartbeat {
								oc.ensureGhostDisplayName(ctx, oc.effectiveModel(meta))
								state.initialEventID = oc.sendInitialStreamMessage(ctx, portal, state.visibleAccumulated.String(), state.turnID, state.replyTarget)
								if state.initialEventID == "" {
									errText := "failed to send initial streaming message (continuation)"
									log.Error().Msg("Failed to send initial streaming message (continuation)")
									state.finishReason = "error"
									oc.emitUIError(ctx, portal, state, errText)
									oc.emitUIFinish(ctx, portal, state, meta)
									return false, nil, &PreDeltaError{Err: errors.New(errText)}
								}
							}
						}
						oc.emitUITextDelta(ctx, portal, state, cleaned)
					}
				}

			case "response.reasoning_text.delta":
				touchTyping()
				if typingSignals != nil {
					typingSignals.SignalReasoningDelta()
				}
				state.reasoning.WriteString(streamEvent.Delta)
				if state.firstToken && state.reasoning.Len() > 0 {
					state.firstToken = false
					state.firstTokenAtMs = time.Now().UnixMilli()
					if !state.suppressSend && !isHeartbeat {
						oc.ensureGhostDisplayName(ctx, oc.effectiveModel(meta))
						state.initialEventID = oc.sendInitialStreamMessage(ctx, portal, "...", state.turnID, state.replyTarget)
						if state.initialEventID == "" {
							errText := "failed to send initial streaming message (continuation)"
							log.Error().Msg("Failed to send initial streaming message (continuation)")
							state.finishReason = "error"
							oc.emitUIError(ctx, portal, state, errText)
							oc.emitUIFinish(ctx, portal, state, meta)
							return false, nil, &PreDeltaError{Err: errors.New(errText)}
						}
					}
				}
				oc.emitUIReasoningDelta(ctx, portal, state, streamEvent.Delta)

			case "response.reasoning_summary_text.delta":
				if strings.TrimSpace(streamEvent.Delta) != "" {
					state.reasoning.WriteString(streamEvent.Delta)
					oc.emitUIReasoningDelta(ctx, portal, state, streamEvent.Delta)
				}

			case "response.reasoning_text.done", "response.reasoning_summary_text.done":
				if strings.TrimSpace(streamEvent.Text) != "" {
					state.reasoning.WriteString(streamEvent.Text)
					oc.emitUIReasoningDelta(ctx, portal, state, streamEvent.Text)
				}

			case "response.refusal.delta":
				touchTyping()
				if typingSignals != nil {
					typingSignals.SignalTextDelta(streamEvent.Delta)
				}
				oc.emitUITextDelta(ctx, portal, state, streamEvent.Delta)

			case "response.refusal.done":
				if strings.TrimSpace(streamEvent.Refusal) != "" {
					oc.emitUITextDelta(ctx, portal, state, streamEvent.Refusal)
				}

			case "response.output_text.done":
				// text-end is emitted from emitUIFinish to keep one contiguous part.

			case "response.output_text.annotation.added":
				if citation, ok := extractURLCitation(streamEvent.Annotation); ok {
					state.sourceCitations = mergeSourceCitations(state.sourceCitations, []sourceCitation{citation})
					oc.emitUISourceURL(ctx, portal, state, citation)
				}
				if document, ok := extractDocumentCitation(streamEvent.Annotation); ok {
					state.sourceDocuments = append(state.sourceDocuments, document)
					oc.emitUISourceDocument(ctx, portal, state, document)
				}
				oc.emitStreamEvent(ctx, portal, state, map[string]any{
					"type":      "data-annotation",
					"data":      map[string]any{"annotation": streamEvent.Annotation, "index": streamEvent.AnnotationIndex},
					"transient": true,
				})

			case "response.function_call_arguments.delta":
				touchTyping()
				if typingSignals != nil {
					typingSignals.SignalToolStart()
				}
				tool, exists := activeTools[streamEvent.ItemID]
				if !exists {
					callID := streamEvent.ItemID
					if strings.TrimSpace(callID) == "" {
						callID = NewCallID()
					}
					tool = &activeToolCall{
						callID:      callID,
						toolName:    streamEvent.Name,
						toolType:    ToolTypeFunction,
						startedAtMs: time.Now().UnixMilli(),
					}
					activeTools[streamEvent.ItemID] = tool
					if state.initialEventID == "" && !state.suppressSend {
						oc.ensureGhostDisplayName(ctx, oc.effectiveModel(meta))
					}
					if strings.TrimSpace(tool.toolName) != "" {
						tool.eventID = oc.sendToolCallEvent(ctx, portal, state, tool)
					}
				}
				tool.input.WriteString(streamEvent.Delta)
				oc.emitUIToolInputDelta(ctx, portal, state, tool.callID, streamEvent.Name, streamEvent.Delta, tool.toolType == ToolTypeProvider)

			case "response.function_call_arguments.done":
				touchTyping()
				if typingSignals != nil {
					typingSignals.SignalToolStart()
				}
				tool, exists := activeTools[streamEvent.ItemID]
				if !exists {
					callID := streamEvent.ItemID
					if strings.TrimSpace(callID) == "" {
						callID = NewCallID()
					}
					tool = &activeToolCall{
						callID:      callID,
						toolName:    streamEvent.Name,
						toolType:    ToolTypeFunction,
						startedAtMs: time.Now().UnixMilli(),
					}
					tool.input.WriteString(streamEvent.Arguments)
					activeTools[streamEvent.ItemID] = tool
				}

				tool.itemID = streamEvent.ItemID

				toolName := strings.TrimSpace(tool.toolName)
				if toolName == "" {
					toolName = strings.TrimSpace(streamEvent.Name)
				}
				tool.toolName = toolName
				if tool.eventID == "" {
					tool.eventID = oc.sendToolCallEvent(ctx, portal, state, tool)
				}
				argsJSON := strings.TrimSpace(tool.input.String())
				if argsJSON == "" {
					argsJSON = strings.TrimSpace(streamEvent.Arguments)
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
					argsObj, _ := inputMap.(map[string]any)
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

					if resultStatus != ResultStatusDenied {
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
							log.Warn().Err(err).Str("tool", toolName).Msg("Tool execution failed (continuation)")
							result = fmt.Sprintf("Error: %s", err.Error())
							resultStatus = ResultStatusError
						}
					}
				}

				// Check for TTS audio result (AUDIO: prefix)
				displayResult := result
				if strings.HasPrefix(result, TTSResultPrefix) {
					audioB64 := strings.TrimPrefix(result, TTSResultPrefix)
					audioData, err := base64.StdEncoding.DecodeString(audioB64)
					if err != nil {
						log.Warn().Err(err).Msg("Failed to decode TTS audio (continuation)")
						displayResult = "Error: failed to decode TTS audio"
						resultStatus = ResultStatusError
					} else {
						mimeType := detectAudioMime(audioData, "audio/mpeg")
						if _, mediaURL, err := oc.sendGeneratedAudio(ctx, portal, audioData, mimeType, state.turnID); err != nil {
							log.Warn().Err(err).Msg("Failed to send TTS audio (continuation)")
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

				// Check for image generation result (IMAGE: / IMAGES: prefix)
				if strings.HasPrefix(result, ImagesResultPrefix) {
					payload := strings.TrimPrefix(result, ImagesResultPrefix)
					var images []string
					if err := json.Unmarshal([]byte(payload), &images); err != nil {
						log.Warn().Err(err).Msg("Failed to parse generated images payload (continuation)")
						displayResult = "Error: failed to parse generated images"
						resultStatus = ResultStatusError
					} else {
						success := 0
						var sentURLs []string
						for _, imageB64 := range images {
							imageData, mimeType, err := decodeBase64Image(imageB64)
							if err != nil {
								log.Warn().Err(err).Msg("Failed to decode generated image (continuation)")
								continue
							}
							_, mediaURL, err := oc.sendGeneratedImage(ctx, portal, imageData, mimeType, state.turnID, imageCaption)
							if err != nil {
								log.Warn().Err(err).Msg("Failed to send generated image (continuation)")
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
						log.Warn().Err(err).Msg("Failed to decode generated image (continuation)")
						displayResult = "Error: failed to decode generated image"
						resultStatus = ResultStatusError
					} else {
						if _, mediaURL, err := oc.sendGeneratedImage(ctx, portal, imageData, mimeType, state.turnID, imageCaption); err != nil {
							log.Warn().Err(err).Msg("Failed to send generated image (continuation)")
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

				tool.result = result
				collectToolOutputCitations(state, toolName, result)
				state.pendingFunctionOutputs = append(state.pendingFunctionOutputs, functionCallOutput{
					callID:    streamEvent.ItemID,
					name:      toolName,
					arguments: argsJSON,
					output:    result,
				})

				// Emit UI tool output immediately so the desktop sees the tool
				// as completed without waiting for the timeline event send.
				if resultStatus == ResultStatusSuccess {
					oc.emitUIToolOutputAvailable(ctx, portal, state, tool.callID, result, tool.toolType == ToolTypeProvider, false)
				} else if resultStatus != ResultStatusDenied {
					oc.emitUIToolOutputError(ctx, portal, state, tool.callID, result, tool.toolType == ToolTypeProvider)
				}

				inputMapForMeta := map[string]any{}
				if parsed, ok := inputMap.(map[string]any); ok {
					inputMapForMeta = parsed
				} else if raw, ok := inputMap.(string); ok && raw != "" {
					inputMapForMeta = map[string]any{"_raw": raw}
				}

				// Track tool call in metadata (sendToolResultEvent is a blocking
				// Matrix API call, but the UI update was already emitted above).
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

			case "response.completed":
				state.completedAtMs = time.Now().UnixMilli()
				if streamEvent.Response.Usage.TotalTokens > 0 || streamEvent.Response.Usage.InputTokens > 0 || streamEvent.Response.Usage.OutputTokens > 0 {
					state.promptTokens = streamEvent.Response.Usage.InputTokens
					state.completionTokens = streamEvent.Response.Usage.OutputTokens
					state.reasoningTokens = streamEvent.Response.Usage.OutputTokensDetails.ReasoningTokens
					state.totalTokens = streamEvent.Response.Usage.TotalTokens
				}
				if streamEvent.Response.Status == "completed" {
					state.finishReason = "stop"
				} else {
					state.finishReason = string(streamEvent.Response.Status)
				}
				if streamEvent.Response.ID != "" {
					state.responseID = streamEvent.Response.ID
				}
				oc.emitUIMessageMetadata(ctx, portal, state, oc.buildUIMessageMetadata(state, meta, true))
				log.Debug().Str("reason", state.finishReason).Str("response_id", state.responseID).Msg("Continuation stream completed")

			case "error":
				apiErr := fmt.Errorf("API error: %s", streamEvent.Message)
				state.finishReason = "error"
				oc.emitUIError(ctx, portal, state, streamEvent.Message)
				oc.emitUIFinish(ctx, portal, state, meta)
				logResponsesFailure(log, apiErr, continuationParams, meta, messages, "continuation_event_error")
				if state.initialEventID != "" {
					return false, nil, &NonFallbackError{Err: apiErr}
				}
				return false, nil, &PreDeltaError{Err: apiErr}
			default:
				// Ignore unknown events
			}
		}

		oc.emitUIStepFinish(ctx, portal, state)

		if err := stream.Err(); err != nil {
			logResponsesFailure(log, err, continuationParams, meta, messages, "continuation_err")
			if errors.Is(err, context.Canceled) {
				state.finishReason = "cancelled"
				if state.initialEventID != "" && state.accumulated.Len() > 0 {
					oc.flushPartialStreamingMessage(context.Background(), portal, state, meta)
				}
				oc.emitUIAbort(ctx, portal, state, "cancelled")
				oc.emitUIFinish(ctx, portal, state, meta)
				if state.initialEventID != "" {
					return false, nil, &NonFallbackError{Err: err}
				}
				return false, nil, &PreDeltaError{Err: err}
			}
			state.finishReason = "error"
			oc.emitUIError(ctx, portal, state, err.Error())
			oc.emitUIFinish(ctx, portal, state, meta)
			if state.initialEventID != "" {
				return false, nil, &NonFallbackError{Err: err}
			}
			return false, nil, &PreDeltaError{Err: err}
		}
	}

	if state.finishReason == "" {
		state.finishReason = "stop"
	}

	// Send any generated images as separate messages
	for _, img := range state.pendingImages {
		imageData, mimeType, err := decodeBase64Image(img.imageB64)
		if err != nil {
			log.Warn().Err(err).Str("item_id", img.itemID).Msg("Failed to decode generated image")
			continue
		}
		// Native API image generation — no user-provided prompt available for caption.
		eventID, mediaURL, err := oc.sendGeneratedImage(ctx, portal, imageData, mimeType, img.turnID, "")
		if err != nil {
			log.Warn().Err(err).Str("item_id", img.itemID).Msg("Failed to send generated image to Matrix")
			continue
		}
		recordGeneratedFile(state, mediaURL, mimeType)
		oc.emitUIFile(ctx, portal, state, mediaURL, mimeType)
		log.Info().Stringer("event_id", eventID).Str("item_id", img.itemID).Msg("Sent generated image to Matrix")
	}
	oc.emitUIFinish(ctx, portal, state, meta)

	// Send final message to persist complete content with metadata (including reasoning)
	if state.initialEventID != "" || state.heartbeat != nil {
		oc.sendFinalAssistantTurn(ctx, portal, state, meta)
		if state.initialEventID != "" && !state.suppressSave {
			oc.saveAssistantMessage(ctx, log, portal, state, meta)
		}
	}

	log.Info().
		Str("turn_id", state.turnID).
		Str("finish_reason", state.finishReason).
		Int("content_length", state.accumulated.Len()).
		Int("reasoning_length", state.reasoning.Len()).
		Int("tool_calls", len(state.toolCalls)).
		Str("response_id", state.responseID).
		Int("images_sent", len(state.pendingImages)).
		Msg("Responses API streaming finished")

	// Generate room title after first response
	oc.maybeGenerateTitle(ctx, portal, state.accumulated.String())

	oc.recordProviderSuccess(ctx)
	return true, nil, nil
}
