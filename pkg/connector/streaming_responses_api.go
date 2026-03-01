package connector

import (
	"context"
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

	prep, messages, typingCleanup := oc.prepareStreamingRun(ctx, log, evt, portal, meta, messages)
	defer typingCleanup()
	state := prep.State
	typingSignals := prep.TypingSignals
	touchTyping := prep.TouchTyping
	isHeartbeat := state.heartbeat != nil

	if state.roomID != "" {
		oc.markRoomRunStreaming(state.roomID, true)
		defer oc.markRoomRunStreaming(state.roomID, false)
	}

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
		case "response.created", "response.queued", "response.in_progress", "response.failed", "response.incomplete":
			oc.handleResponseLifecycleEvent(ctx, portal, state, meta, streamEvent.Type, streamEvent.Response)

		case "response.output_item.added":
			oc.handleResponseOutputItemAdded(ctx, portal, state, activeTools, streamEvent.Item)

		case "response.output_item.done":
			oc.handleResponseOutputItemDone(ctx, portal, state, activeTools, streamEvent.Item)

		case "response.custom_tool_call_input.delta":
			oc.handleCustomToolInputDeltaFromOutputItem(ctx, portal, state, activeTools, streamEvent.ItemID, streamEvent.Item, streamEvent.Delta)

		case "response.custom_tool_call_input.done":
			oc.handleCustomToolInputDoneFromOutputItem(ctx, portal, state, activeTools, streamEvent.ItemID, streamEvent.Item, streamEvent.Input)

		case "response.code_interpreter_call_code.delta":
			oc.handleProviderToolInputDeltaFromOutputItem(ctx, portal, state, activeTools, streamEvent.ItemID, streamEvent.Item, streamEvent.Delta)

		case "response.code_interpreter_call_code.done":
			oc.handleProviderToolInputDoneFromOutputItem(ctx, portal, state, activeTools, streamEvent.ItemID, streamEvent.Item, streamEvent.Code)

		case "response.mcp_call_arguments.delta":
			oc.handleProviderToolInputDeltaFromOutputItem(ctx, portal, state, activeTools, streamEvent.ItemID, streamEvent.Item, streamEvent.Delta)

		case "response.mcp_call_arguments.done":
			oc.handleProviderToolInputDoneFromOutputItem(ctx, portal, state, activeTools, streamEvent.ItemID, streamEvent.Item, streamEvent.Arguments)

		case "response.mcp_call.failed":
			oc.handleMCPCallFailedFromOutputItem(ctx, portal, state, activeTools, streamEvent.ItemID, streamEvent.Item)

		case "response.output_text.delta":
			touchTyping()
			if err := oc.handleResponseOutputTextDelta(
				ctx,
				log,
				portal,
				state,
				meta,
				typingSignals,
				isHeartbeat,
				streamEvent.Delta,
				"failed to send initial streaming message",
				"Failed to send initial streaming message",
			); err != nil {
				return false, nil, &PreDeltaError{Err: err}
			}

		case "response.reasoning_text.delta":
			touchTyping()
			if typingSignals != nil {
				typingSignals.SignalReasoningDelta()
			}
			if err := oc.handleResponseReasoningTextDelta(
				ctx,
				log,
				portal,
				state,
				meta,
				isHeartbeat,
				streamEvent.Delta,
				"failed to send initial streaming message",
				"Failed to send initial streaming message",
			); err != nil {
				return false, nil, &PreDeltaError{Err: err}
			}

		case "response.reasoning_summary_text.delta":
			oc.handleResponseReasoningSummaryDelta(ctx, portal, state, strings.TrimSpace(streamEvent.Delta))

		case "response.reasoning_text.done", "response.reasoning_summary_text.done":
			oc.handleResponseReasoningDone(ctx, portal, state, strings.TrimSpace(streamEvent.Text))

		case "response.refusal.delta":
			touchTyping()
			oc.handleResponseRefusalDelta(ctx, portal, state, typingSignals, streamEvent.Delta)

		case "response.refusal.done":
			oc.handleResponseRefusalDone(ctx, portal, state, strings.TrimSpace(streamEvent.Refusal))

		case "response.output_text.done":
			// text-end is emitted from emitUIFinish to keep one contiguous part.
		case "response.function_call_arguments.delta":
			touchTyping()
			if typingSignals != nil {
				typingSignals.SignalToolStart()
			}
			oc.handleFunctionCallArgumentsDelta(ctx, portal, state, meta, activeTools, streamEvent.ItemID, streamEvent.Name, streamEvent.Delta)

		case "response.function_call_arguments.done":
			touchTyping()
			if typingSignals != nil {
				typingSignals.SignalToolStart()
			}
			oc.handleFunctionCallArgumentsDone(ctx, log, portal, state, meta, activeTools, streamEvent.ItemID, streamEvent.Name, streamEvent.Arguments, true, "")

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
			oc.handleResponseOutputAnnotationAdded(ctx, portal, state, streamEvent.Annotation, streamEvent.AnnotationIndex)

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
		cle, handledErr := oc.handleResponsesStreamErr(ctx, portal, state, meta, err, true)
		if cle != nil {
			return false, cle, nil
		}
		return false, nil, handledErr
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
			case "response.created", "response.queued", "response.in_progress", "response.failed", "response.incomplete":
				oc.handleResponseLifecycleEvent(ctx, portal, state, meta, streamEvent.Type, streamEvent.Response)

			case "response.output_item.added":
				oc.handleResponseOutputItemAdded(ctx, portal, state, activeTools, streamEvent.Item)

			case "response.output_item.done":
				oc.handleResponseOutputItemDone(ctx, portal, state, activeTools, streamEvent.Item)

			case "response.custom_tool_call_input.delta":
				oc.handleCustomToolInputDeltaFromOutputItem(ctx, portal, state, activeTools, streamEvent.ItemID, streamEvent.Item, streamEvent.Delta)

			case "response.custom_tool_call_input.done":
				oc.handleCustomToolInputDoneFromOutputItem(ctx, portal, state, activeTools, streamEvent.ItemID, streamEvent.Item, streamEvent.Input)

			case "response.code_interpreter_call_code.delta":
				oc.handleProviderToolInputDeltaFromOutputItem(ctx, portal, state, activeTools, streamEvent.ItemID, streamEvent.Item, streamEvent.Delta)

			case "response.code_interpreter_call_code.done":
				oc.handleProviderToolInputDoneFromOutputItem(ctx, portal, state, activeTools, streamEvent.ItemID, streamEvent.Item, streamEvent.Code)

			case "response.mcp_call_arguments.delta":
				oc.handleProviderToolInputDeltaFromOutputItem(ctx, portal, state, activeTools, streamEvent.ItemID, streamEvent.Item, streamEvent.Delta)

			case "response.mcp_call_arguments.done":
				oc.handleProviderToolInputDoneFromOutputItem(ctx, portal, state, activeTools, streamEvent.ItemID, streamEvent.Item, streamEvent.Arguments)

			case "response.mcp_call.failed":
				oc.handleMCPCallFailedFromOutputItem(ctx, portal, state, activeTools, streamEvent.ItemID, streamEvent.Item)

			case "response.output_text.delta":
				touchTyping()
				if err := oc.handleResponseOutputTextDelta(
					ctx,
					log,
					portal,
					state,
					meta,
					typingSignals,
					isHeartbeat,
					streamEvent.Delta,
					"failed to send initial streaming message (continuation)",
					"Failed to send initial streaming message (continuation)",
				); err != nil {
					return false, nil, &PreDeltaError{Err: err}
				}

			case "response.reasoning_text.delta":
				touchTyping()
				if typingSignals != nil {
					typingSignals.SignalReasoningDelta()
				}
				if err := oc.handleResponseReasoningTextDelta(
					ctx,
					log,
					portal,
					state,
					meta,
					isHeartbeat,
					streamEvent.Delta,
					"failed to send initial streaming message (continuation)",
					"Failed to send initial streaming message (continuation)",
				); err != nil {
					return false, nil, &PreDeltaError{Err: err}
				}

			case "response.reasoning_summary_text.delta":
				oc.handleResponseReasoningSummaryDelta(ctx, portal, state, strings.TrimSpace(streamEvent.Delta))

			case "response.reasoning_text.done", "response.reasoning_summary_text.done":
				oc.handleResponseReasoningDone(ctx, portal, state, strings.TrimSpace(streamEvent.Text))

			case "response.refusal.delta":
				touchTyping()
				oc.handleResponseRefusalDelta(ctx, portal, state, typingSignals, streamEvent.Delta)

			case "response.refusal.done":
				oc.handleResponseRefusalDone(ctx, portal, state, strings.TrimSpace(streamEvent.Refusal))

			case "response.output_text.done":
				// text-end is emitted from emitUIFinish to keep one contiguous part.

			case "response.output_text.annotation.added":
				oc.handleResponseOutputAnnotationAdded(ctx, portal, state, streamEvent.Annotation, streamEvent.AnnotationIndex)

			case "response.function_call_arguments.delta":
				touchTyping()
				if typingSignals != nil {
					typingSignals.SignalToolStart()
				}
				oc.handleFunctionCallArgumentsDelta(ctx, portal, state, meta, activeTools, streamEvent.ItemID, streamEvent.Name, streamEvent.Delta)

			case "response.function_call_arguments.done":
				touchTyping()
				if typingSignals != nil {
					typingSignals.SignalToolStart()
				}
				oc.handleFunctionCallArgumentsDone(ctx, log, portal, state, meta, activeTools, streamEvent.ItemID, streamEvent.Name, streamEvent.Arguments, false, " (continuation)")

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
			_, handledErr := oc.handleResponsesStreamErr(ctx, portal, state, meta, err, false)
			return false, nil, handledErr
		}
	}

	oc.finalizeResponsesStream(ctx, log, portal, state, meta)
	return true, nil, nil
}
