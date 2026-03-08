package connector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/shared/constant"
	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"

	runtimeparse "github.com/beeper/ai-bridge/pkg/runtime"

	"github.com/beeper/ai-bridge/pkg/agents/tools"
)

func (oc *AIClient) streamChatCompletions(
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
		Str("action", "stream_chat_completions").
		Str("portal", portalID).
		Logger()

	prep, messages, typingCleanup := oc.prepareStreamingRun(ctx, log, evt, portal, meta, messages)
	defer typingCleanup()
	state := prep.State
	typingSignals := prep.TypingSignals
	touchTyping := prep.TouchTyping
	isHeartbeat := prep.IsHeartbeat

	currentMessages := messages
	// Tool loops can legitimately require several rounds (e.g. multi-step file ops).
	// Keep a cap to prevent runaway loops, but 3 rounds is too low in practice.
	maxToolRounds := 10

	oc.emitUIStart(ctx, portal, state, meta)

	for round := 0; ; round++ {
		params := openai.ChatCompletionNewParams{
			Model:    oc.effectiveModelForAPI(meta),
			Messages: currentMessages,
		}
		params.StreamOptions = openai.ChatCompletionStreamOptionsParam{
			IncludeUsage: param.NewOpt(true),
		}
		if maxTokens := oc.effectiveMaxTokens(meta); maxTokens > 0 {
			params.MaxCompletionTokens = openai.Int(int64(maxTokens))
		}
		if temp := oc.effectiveTemperature(meta); temp > 0 {
			params.Temperature = openai.Float(temp)
		}
		// Add builtin tools for this turn.
		// In simple mode this is intentionally restricted to web_search.
		enabledTools := oc.selectedBuiltinToolsForTurn(ctx, meta)
		chatHasAgent := resolveAgentID(meta) != ""
		if len(enabledTools) > 0 {
			params.Tools = append(params.Tools, ToOpenAIChatTools(enabledTools, &oc.log)...)
		}
		if meta.Capabilities.SupportsToolCalling && chatHasAgent {
			if !oc.isBuilderRoom(portal) {
				var enabledSessions []*tools.Tool
				for _, tool := range tools.SessionTools() {
					if oc.isToolEnabled(meta, tool.Name) {
						enabledSessions = append(enabledSessions, tool)
					}
				}
				if len(enabledSessions) > 0 {
					params.Tools = append(params.Tools, bossToolsToChatTools(enabledSessions, &oc.log)...)
				}
			}
			if hasBossAgent(meta) || oc.isBuilderRoom(portal) {
				var enabledBoss []*tools.Tool
				for _, tool := range tools.BossTools() {
					if oc.isToolEnabled(meta, tool.Name) {
						enabledBoss = append(enabledBoss, tool)
					}
				}
				params.Tools = append(params.Tools, bossToolsToChatTools(enabledBoss, &oc.log)...)
			}
			params.Tools = dedupeChatToolParams(params.Tools)
		}

		stream := oc.api.Chat.Completions.NewStreaming(ctx, params)
		if stream == nil {
			initErr := errors.New("chat completions streaming not available")
			logChatCompletionsFailure(log, initErr, params, meta, currentMessages, "stream_init")
			return false, nil, &PreDeltaError{Err: initErr}
		}

		// Track active tool calls by index
		activeTools := make(map[int]*activeToolCall)
		var roundContent strings.Builder
		state.finishReason = ""

		oc.uiEmitter(state).EmitUIStepStart(ctx, portal)

		for stream.Next() {
			chunk := stream.Current()
			oc.markMessageSendSuccess(ctx, portal, evt, state)

			if chunk.Usage.TotalTokens > 0 || chunk.Usage.PromptTokens > 0 || chunk.Usage.CompletionTokens > 0 {
				state.promptTokens = chunk.Usage.PromptTokens
				state.completionTokens = chunk.Usage.CompletionTokens
				state.reasoningTokens = chunk.Usage.CompletionTokensDetails.ReasoningTokens
				state.totalTokens = chunk.Usage.TotalTokens
				oc.uiEmitter(state).EmitUIMessageMetadata(ctx, portal, oc.buildUIMessageMetadata(state, meta, true))
			}

			for _, choice := range chunk.Choices {
				if choice.Delta.Content != "" {
					touchTyping()
					delta := maybePrependTextSeparator(state, choice.Delta.Content)
					state.accumulated.WriteString(delta)
					roundContent.WriteString(delta)

					parsed := (*runtimeparse.StreamingDirectiveResult)(nil)
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
									state.initialEventID = oc.sendInitialStreamMessage(ctx, portal, state, state.visibleAccumulated.String(), state.turnID, state.replyTarget)
									if !state.hasInitialMessageTarget() {
										errText := "failed to send initial streaming message"
										log.Error().Msg("Failed to send initial streaming message")
										state.finishReason = "error"
										oc.uiEmitter(state).EmitUIError(ctx, portal, errText)
										oc.emitUIFinish(ctx, portal, state, meta)
										return false, nil, &PreDeltaError{Err: errors.New(errText)}
									}
								}
							}
							oc.uiEmitter(state).EmitUITextDelta(ctx, portal, cleaned)
						}
					}
				}

				if choice.Delta.Refusal != "" {
					touchTyping()
					if typingSignals != nil {
						typingSignals.SignalTextDelta(choice.Delta.Refusal)
					}
					oc.uiEmitter(state).EmitUITextDelta(ctx, portal, choice.Delta.Refusal)
				}

				// Handle tool calls from Chat Completions API
				for _, toolDelta := range choice.Delta.ToolCalls {
					touchTyping()
					if typingSignals != nil {
						typingSignals.SignalToolStart()
					}
					toolIdx := int(toolDelta.Index)
					tool, exists := activeTools[toolIdx]
					if !exists {
						callID := toolDelta.ID
						if strings.TrimSpace(callID) == "" {
							callID = NewCallID()
						}
						tool = &activeToolCall{
							callID:      callID,
							toolType:    ToolTypeFunction,
							startedAtMs: time.Now().UnixMilli(),
						}
						activeTools[toolIdx] = tool
					}

					// Capture tool ID if provided (used by OpenAI for tracking)
					if toolDelta.ID != "" && tool.callID == "" {
						tool.callID = toolDelta.ID
					}

					// Update tool name if provided in this delta
					if toolDelta.Function.Name != "" {
						tool.toolName = toolDelta.Function.Name
						if tool.eventID == "" {
							tool.eventID = oc.sendToolCallEvent(ctx, portal, state, tool)
						}
					}

					// Accumulate arguments
					if toolDelta.Function.Arguments != "" {
						tool.input.WriteString(toolDelta.Function.Arguments)
						oc.uiEmitter(state).EmitUIToolInputDelta(ctx, portal, tool.callID, tool.toolName, toolDelta.Function.Arguments, false)
					}
				}

				if choice.FinishReason != "" {
					state.finishReason = string(choice.FinishReason)
				}
			}

		}

		oc.uiEmitter(state).EmitUIStepFinish(ctx, portal)

		if err := stream.Err(); err != nil {
			if errors.Is(err, context.Canceled) {
				state.finishReason = "cancelled"
				if state.hasInitialMessageTarget() && state.accumulated.Len() > 0 {
					oc.flushPartialStreamingMessage(context.Background(), portal, state, meta)
				}
				oc.uiEmitter(state).EmitUIAbort(ctx, portal, "cancelled")
				oc.emitUIFinish(ctx, portal, state, meta)
				return false, nil, streamFailureError(state, err)
			}
			if cle := ParseContextLengthError(err); cle != nil {
				return false, cle, nil
			}
			logChatCompletionsFailure(log, err, params, meta, currentMessages, "stream_err")
			state.finishReason = "error"
			oc.uiEmitter(state).EmitUIError(ctx, portal, err.Error())
			oc.emitUIFinish(ctx, portal, state, meta)
			return false, nil, streamFailureError(state, err)
		}

		// Execute any accumulated tool calls
		type chatToolResult struct {
			callID string
			output string
		}
		toolCallParams := make([]openai.ChatCompletionMessageToolCallUnionParam, 0, len(activeTools))
		toolResults := make([]chatToolResult, 0, len(activeTools))

		if len(activeTools) > 0 {
			keys := make([]int, 0, len(activeTools))
			for key := range activeTools {
				keys = append(keys, key)
			}
			sort.Ints(keys)
			for _, key := range keys {
				tool := activeTools[key]
				if tool == nil {
					continue
				}
				if tool.callID == "" {
					tool.callID = NewCallID()
				}
				toolName := strings.TrimSpace(tool.toolName)
				if toolName == "" {
					toolName = "unknown_tool"
				}
				if tool.eventID == "" {
					tool.toolName = toolName
					tool.eventID = oc.sendToolCallEvent(ctx, portal, state, tool)
				}

				argsJSON := normalizeToolArgsJSON(tool.input.String())
				toolCallParams = append(toolCallParams, openai.ChatCompletionMessageToolCallUnionParam{
					OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
						ID: tool.callID,
						Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
							Name:      toolName,
							Arguments: argsJSON,
						},
						Type: constant.ValueOf[constant.Function](),
					},
				})

				touchTyping()
				if typingSignals != nil {
					typingSignals.SignalToolStart()
				}
				// Wrap context with bridge info for tools that need it (e.g., channel-edit, react)
				toolCtx := WithBridgeToolContext(ctx, &BridgeToolContext{
					Client:        oc,
					Portal:        portal,
					Meta:          meta,
					SourceEventID: state.sourceEventID,
					SenderID:      state.senderID,
				})

				result := ""
				resultStatus := ResultStatusSuccess
				if !oc.isToolEnabled(meta, toolName) {
					result = fmt.Sprintf("Error: tool %s is not enabled", toolName)
					resultStatus = ResultStatusError
				} else {
					// Tool approval gating for dangerous builtin tools.
					var argsObj map[string]any
					_ = json.Unmarshal([]byte(argsJSON), &argsObj)
					if oc.isBuiltinToolDenied(ctx, portal, state, tool, toolName, argsObj) {
						resultStatus = ResultStatusDenied
						result = "Denied by user"
					}

					if resultStatus != ResultStatusDenied {
						var err error
						result, err = oc.executeBuiltinTool(toolCtx, portal, toolName, argsJSON)
						if err != nil {
							log.Warn().Err(err).Str("tool", toolName).Msg("Tool execution failed (Chat Completions)")
							result = fmt.Sprintf("Error: %s", err.Error())
							resultStatus = ResultStatusError
						}
					}

					result, resultStatus = oc.processToolMediaResult(ctx, log, portal, state, argsJSON, result, resultStatus, " (Chat Completions)")
				}

				// Normalize input for storage
				var inputMap any
				if err := json.Unmarshal([]byte(argsJSON), &inputMap); err != nil {
					inputMap = argsJSON
					oc.uiEmitter(state).EmitUIToolInputError(ctx, portal, tool.callID, toolName, argsJSON, "Invalid JSON tool input", false, false)
				}
				oc.uiEmitter(state).EmitUIToolInputAvailable(ctx, portal, tool.callID, toolName, inputMap, false)

				recordCompletedToolCall(ctx, oc, portal, state, tool, toolName, argsJSON, result, resultStatus)

				if resultStatus == ResultStatusSuccess {
					collectToolOutputCitations(state, toolName, result)
					oc.uiEmitter(state).EmitUIToolOutputAvailable(ctx, portal, tool.callID, result, tool.toolType == ToolTypeProvider, false)
				} else if resultStatus != ResultStatusDenied {
					oc.uiEmitter(state).EmitUIToolOutputError(ctx, portal, tool.callID, result, tool.toolType == ToolTypeProvider)
				}

				toolResults = append(toolResults, chatToolResult{callID: tool.callID, output: result})
			}
		}

		// Continue if tools were requested.
		// Some Anthropic-compatible adapters may emit `tool_use` (or omit finish reason)
		// even when tool calls are present.
		if shouldContinueChatToolLoop(state.finishReason, len(toolCallParams)) {
			// Ensure the next assistant text delta can't get glued to the previous text.
			state.needsTextSeparator = true
			if round >= maxToolRounds {
				log.Warn().Int("rounds", round+1).Msg("Max tool call rounds reached; stopping chat completions continuation")
				break
			}
			assistantMsg := openai.ChatCompletionAssistantMessageParam{
				ToolCalls: toolCallParams,
			}
			if content := strings.TrimSpace(roundContent.String()); content != "" {
				assistantMsg.Content.OfString = param.NewOpt(content)
			}
			currentMessages = append(currentMessages, openai.ChatCompletionMessageParamUnion{OfAssistant: &assistantMsg})
			for _, result := range toolResults {
				currentMessages = append(currentMessages, openai.ToolMessage(result.output, result.callID))
			}
			if steerItems := oc.drainSteerQueue(state.roomID); len(steerItems) > 0 {
				for _, item := range steerItems {
					if item.pending.Type != pendingTypeText {
						continue
					}
					prompt := strings.TrimSpace(item.prompt)
					if prompt == "" {
						prompt = item.pending.MessageBody
					}
					prompt = strings.TrimSpace(prompt)
					if prompt == "" {
						continue
					}
					currentMessages = append(currentMessages, openai.UserMessage(prompt))
				}
			}
			continue
		}

		break
	}

	state.completedAtMs = time.Now().UnixMilli()
	if state.finishReason == "" {
		state.finishReason = "stop"
	}
	oc.finalizeStreamingReplyAccumulator(state)
	oc.emitUIFinish(ctx, portal, state, meta)

	// Send final edit and save to database.
	if state.hasInitialMessageTarget() {
		oc.sendFinalAssistantTurn(ctx, portal, state, meta)
		if !state.suppressSave {
			oc.saveAssistantMessage(ctx, log, portal, state, meta)
		}
	}

	log.Info().
		Str("turn_id", state.turnID).
		Str("finish_reason", state.finishReason).
		Int("content_length", state.accumulated.Len()).
		Int("tool_calls", len(state.toolCalls)).
		Msg("Chat Completions streaming finished")

	oc.maybeGenerateTitle(ctx, portal, state.accumulated.String())
	oc.recordProviderSuccess(ctx)
	return true, nil, nil
}

// convertToResponsesInput converts Chat Completion messages to Responses API input items
// Supports native multimodal content: images (ResponseInputImageParam), files/PDFs (ResponseInputFileParam)
// Note: Audio is handled via Chat Completions API fallback (SDK v3.16.0 lacks Responses API audio union support)
