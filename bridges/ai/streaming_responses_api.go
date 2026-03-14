package ai

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/packages/ssestream"
	"github.com/openai/openai-go/v3/responses"
	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

// responseStreamContext holds loop-invariant parameters for processing a Responses API
// stream.  Only streamEvent and isContinuation change per event.
type responseStreamContext struct {
	base        *streamingAdapterBase
	activeTools map[string]*activeToolCall
}

type responsesTurnAdapter struct {
	streamingAdapterBase
	params      responses.ResponseNewParams
	initialized bool
	rsc         *responseStreamContext
}

func (a *responsesTurnAdapter) TrackRoomRunStreaming() bool {
	return true
}

func (a *responsesTurnAdapter) startInitialRound(ctx context.Context) (*ssestream.Stream[responses.ResponseStreamEventUnion], error) {
	if !a.initialized {
		a.params = a.oc.buildResponsesAPIParams(ctx, a.portal, a.meta, a.messages)
		if a.oc.isOpenRouterProvider() {
			ctx = WithPDFEngine(ctx, a.oc.effectivePDFEngine(a.meta))
		}
		a.initialized = true
	}
	stream := a.oc.api.Responses.NewStreaming(ctx, a.params)
	if stream == nil {
		return nil, errors.New("responses streaming not available")
	}
	if a.params.Input.OfInputItemList != nil {
		a.state.baseInput = a.params.Input.OfInputItemList
	}
	return stream, nil
}

func (a *responsesTurnAdapter) startContinuationRound(ctx context.Context) (*ssestream.Stream[responses.ResponseStreamEventUnion], responses.ResponseNewParams, error) {
	state := a.state
	if ctx.Err() != nil {
		if state.hasInitialMessageTarget() && state.accumulated.Len() > 0 {
			a.oc.flushPartialStreamingMessage(context.Background(), a.portal, state, a.meta)
		}
		return nil, responses.ResponseNewParams{}, ctx.Err()
	}
	pendingOutputs := slices.Clone(state.pendingFunctionOutputs)
	pendingApprovals := slices.Clone(state.pendingMcpApprovals)

	approvalInputs := make([]responses.ResponseInputItemUnionParam, 0, len(pendingApprovals))
	for _, approval := range pendingApprovals {
		decision := a.oc.waitForToolApprovalDecision(ctx, a.portal, state, approval.approvalID, approval.toolCallID)
		approved := approvalAllowed(decision)
		item := responses.ResponseInputItemParamOfMcpApprovalResponse(approval.approvalID, approved)
		if decision.Reason != "" && item.OfMcpApprovalResponse != nil {
			item.OfMcpApprovalResponse.Reason = param.NewOpt(decision.Reason)
		}
		approvalInputs = append(approvalInputs, item)
	}

	continuationParams := a.oc.buildContinuationParams(ctx, state, a.meta, pendingOutputs, approvalInputs)
	if len(state.baseInput) > 0 {
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

	state.needsTextSeparator = true
	stream := a.oc.api.Responses.NewStreaming(ctx, continuationParams)
	if stream == nil {
		return nil, continuationParams, errors.New("continuation streaming not available")
	}
	state.pendingFunctionOutputs = nil
	state.pendingMcpApprovals = nil
	return stream, continuationParams, nil
}

func (a *responsesTurnAdapter) RunRound(
	ctx context.Context,
	evt *event.Event,
	round int,
) (bool, *ContextLengthError, error) {
	state := a.state
	var (
		stream *ssestream.Stream[responses.ResponseStreamEventUnion]
		params responses.ResponseNewParams
		err    error
	)

	if round == 0 {
		stream, err = a.startInitialRound(ctx)
		params = a.params
		if err != nil {
			logResponsesFailure(a.log, err, params, a.meta, a.messages, "stream_init")
			return false, nil, &PreDeltaError{Err: err}
		}
	} else {
		if len(state.pendingFunctionOutputs) == 0 && len(state.pendingMcpApprovals) == 0 {
			return false, nil, nil
		}
		if round > maxStreamingToolRounds {
			err = fmt.Errorf("max responses tool call rounds reached (%d)", maxStreamingToolRounds)
			a.log.Warn().Err(err).Int("pending_outputs", len(state.pendingFunctionOutputs)).Msg("Stopping responses continuation loop")
			return false, nil, a.oc.finishStreamingWithFailure(ctx, a.log, a.portal, state, a.meta, "error", err)
		}
		a.log.Debug().
			Int("pending_outputs", len(state.pendingFunctionOutputs)).
			Int("pending_approvals", len(state.pendingMcpApprovals)).
			Int("base_input_items", len(state.baseInput)).
			Msg("Continuing stateless response with pending tool actions")
		stream, params, err = a.startContinuationRound(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return false, nil, a.oc.finishStreamingWithFailure(ctx, a.log, a.portal, state, a.meta, "cancelled", err)
			}
			logResponsesFailure(a.log, err, params, a.meta, a.messages, "continuation_init")
			return false, nil, a.oc.finishStreamingWithFailure(ctx, a.log, a.portal, state, a.meta, "error", err)
		}
	}

	activeTools := make(map[string]*activeToolCall)
	a.rsc.activeTools = activeTools
	done, cle, err := runStreamingStep(ctx, a.oc, a.portal, state, evt, stream,
		func(streamEvent responses.ResponseStreamEventUnion) bool { return streamEvent.Type != "error" },
		func(streamEvent responses.ResponseStreamEventUnion) (bool, *ContextLengthError, error) {
			done, cle, evtErr := a.oc.processResponseStreamEvent(ctx, a.rsc, streamEvent, round > 0)
			if done && evtErr != nil {
				stage := "stream_event_error"
				if round > 0 {
					stage = "continuation_event_error"
				}
				logResponsesFailure(a.log, evtErr, params, a.meta, a.messages, stage)
			}
			return done, cle, evtErr
		},
		func(stepErr error) (*ContextLengthError, error) {
			stage := "stream_err"
			if round > 0 {
				stage = "continuation_err"
			}
			logResponsesFailure(a.log, stepErr, params, a.meta, a.messages, stage)
			return a.oc.handleResponsesStreamErr(ctx, a.portal, state, a.meta, stepErr, round == 0)
		},
	)
	if cle != nil || err != nil || done {
		return false, cle, err
	}

	return hasPendingStreamingContinuation(state), nil, nil
}

func (a *responsesTurnAdapter) Finalize(ctx context.Context) {
	a.oc.finalizeResponsesStream(ctx, a.log, a.portal, a.state, a.meta)
}

// processResponseStreamEvent handles a single Responses API stream event.
// Returns done=true when the caller's loop should break (error/fatal), along with
// any context-length error or general error.  The caller is responsible for
// calling logResponsesFailure when err != nil.
func (oc *AIClient) processResponseStreamEvent(
	ctx context.Context,
	rsc *responseStreamContext,
	streamEvent responses.ResponseStreamEventUnion,
	isContinuation bool,
) (done bool, cle *ContextLengthError, err error) {
	log := rsc.base.log
	portal := rsc.base.portal
	state := rsc.base.state
	meta := rsc.base.meta
	activeTools := rsc.activeTools
	typingSignals := rsc.base.typingSignals
	touchTyping := rsc.base.touchTyping
	isHeartbeat := rsc.base.isHeartbeat
	contSuffix := ""
	if isContinuation {
		contSuffix = " (continuation)"
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
		oc.handleCustomToolInputDeltaFromOutputItem(ctx, portal, state, activeTools, streamEvent.ItemID, streamEvent.Item, streamEvent.Delta)

	case "response.code_interpreter_call_code.done":
		oc.handleCustomToolInputDoneFromOutputItem(ctx, portal, state, activeTools, streamEvent.ItemID, streamEvent.Item, streamEvent.Code)

	case "response.mcp_call_arguments.delta":
		oc.handleCustomToolInputDeltaFromOutputItem(ctx, portal, state, activeTools, streamEvent.ItemID, streamEvent.Item, streamEvent.Delta)

	case "response.mcp_call_arguments.done":
		oc.handleCustomToolInputDoneFromOutputItem(ctx, portal, state, activeTools, streamEvent.ItemID, streamEvent.Item, streamEvent.Arguments)

	case "response.mcp_call.failed":
		oc.handleMCPCallFailedFromOutputItem(ctx, portal, state, activeTools, streamEvent.ItemID, streamEvent.Item)

	case "response.output_text.delta":
		touchTyping()
		if err := oc.handleResponseOutputTextDelta(
			ctx, log, portal, state, meta, typingSignals, isHeartbeat,
			streamEvent.Delta,
			"failed to send initial streaming message"+contSuffix,
			"Failed to send initial streaming message"+contSuffix,
		); err != nil {
			return true, nil, &PreDeltaError{Err: err}
		}

	case "response.reasoning_text.delta":
		touchTyping()
		if typingSignals != nil {
			typingSignals.SignalReasoningDelta()
		}
		if err := oc.handleResponseReasoningTextDelta(
			ctx, log, portal, state, meta, isHeartbeat,
			streamEvent.Delta,
			"failed to send initial streaming message"+contSuffix,
			"Failed to send initial streaming message"+contSuffix,
		); err != nil {
			return true, nil, &PreDeltaError{Err: err}
		}

	case "response.reasoning_summary_text.delta":
		oc.appendReasoningText(ctx, portal, state, strings.TrimSpace(streamEvent.Delta))

	case "response.reasoning_text.done", "response.reasoning_summary_text.done":
		oc.appendReasoningText(ctx, portal, state, strings.TrimSpace(streamEvent.Text))

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
		oc.handleFunctionCallArgumentsDone(ctx, log, portal, state, meta, activeTools, streamEvent.ItemID, streamEvent.Name, streamEvent.Arguments, !isContinuation, contSuffix)

	case "response.file_search_call.searching", "response.file_search_call.in_progress":
		oc.handleProviderToolInProgress(ctx, portal, state, meta, activeTools, streamEvent.ItemID, "file_search", ToolTypeProvider)

	case "response.file_search_call.completed":
		oc.handleProviderToolCompleted(ctx, portal, state, activeTools, streamEvent.ItemID, "file_search", ToolTypeProvider, "")

	case "response.code_interpreter_call.in_progress", "response.code_interpreter_call.interpreting":
		oc.handleProviderToolInProgress(ctx, portal, state, meta, activeTools, streamEvent.ItemID, "code_interpreter", ToolTypeProvider)

	case "response.code_interpreter_call.completed":
		oc.handleProviderToolCompleted(ctx, portal, state, activeTools, streamEvent.ItemID, "code_interpreter", ToolTypeProvider, "")

	case "response.mcp_list_tools.in_progress":
		oc.handleProviderToolInProgress(ctx, portal, state, meta, activeTools, streamEvent.ItemID, "mcp.list_tools", ToolTypeMCP)

	case "response.mcp_list_tools.completed":
		oc.handleProviderToolCompleted(ctx, portal, state, activeTools, streamEvent.ItemID, "mcp.list_tools", ToolTypeMCP, "")

	case "response.mcp_list_tools.failed":
		oc.handleProviderToolCompleted(ctx, portal, state, activeTools, streamEvent.ItemID, "mcp.list_tools", ToolTypeMCP, "MCP list tools failed")

	case "response.mcp_call.in_progress":
		oc.handleProviderToolInProgress(ctx, portal, state, meta, activeTools, streamEvent.ItemID, "mcp.call", ToolTypeMCP)

	case "response.mcp_call.completed":
		oc.handleProviderToolCompleted(ctx, portal, state, activeTools, streamEvent.ItemID, "mcp.call", ToolTypeMCP, "")

	case "response.web_search_call.searching", "response.web_search_call.in_progress":
		touchTyping()
		if typingSignals != nil {
			typingSignals.SignalToolStart()
		}
		oc.handleProviderToolInProgress(ctx, portal, state, meta, activeTools, streamEvent.ItemID, "web_search", ToolTypeProvider)

	case "response.web_search_call.completed":
		touchTyping()
		oc.handleProviderToolCompleted(ctx, portal, state, activeTools, streamEvent.ItemID, "web_search", ToolTypeProvider, "")

	case "response.image_generation_call.in_progress", "response.image_generation_call.generating":
		touchTyping()
		if typingSignals != nil {
			typingSignals.SignalToolStart()
		}
		oc.handleProviderToolInProgress(ctx, portal, state, meta, activeTools, streamEvent.ItemID, "image_generation", ToolTypeProvider)
		log.Debug().Str("item_id", streamEvent.ItemID).Msg("Image generation in progress")

	case "response.image_generation_call.completed":
		touchTyping()
		if typingSignals != nil {
			typingSignals.SignalToolStart()
		}
		oc.handleProviderToolCompleted(ctx, portal, state, activeTools, streamEvent.ItemID, "image_generation", ToolTypeProvider, "")
		log.Info().Str("item_id", streamEvent.ItemID).Msg("Image generation completed")

	case "response.image_generation_call.partial_image":
		touchTyping()
		if typingSignals != nil {
			typingSignals.SignalToolStart()
		}
		oc.writer(state, portal).Data(ctx, "image_generation_partial", map[string]any{
			"item_id":   streamEvent.ItemID,
			"index":     streamEvent.PartialImageIndex,
			"image_b64": streamEvent.PartialImageB64,
		}, true)

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
		if streamEvent.Response.ID != "" {
			state.responseID = streamEvent.Response.ID
		}
		oc.writer(state, portal).MessageMetadata(ctx, oc.buildUIMessageMetadata(state, meta, true))

		if !isContinuation {
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
		}
		log.Debug().Str("reason", state.finishReason).Str("response_id", state.responseID).Int("images", len(state.pendingImages)).
			Msg("Response stream completed" + contSuffix)

	case "error":
		apiErr := fmt.Errorf("API error: %s", streamEvent.Message)
		// Check for context length error (only on initial stream, not continuation)
		if !isContinuation {
			if strings.Contains(streamEvent.Message, "context_length") || strings.Contains(streamEvent.Message, "token") {
				return true, &ContextLengthError{
					OriginalError: fmt.Errorf("%s", streamEvent.Message),
				}, nil
			}
		}
		return true, nil, oc.finishStreamingWithFailure(ctx, log, portal, state, meta, "error", apiErr)

	default:
		// Ignore unknown events
	}

	return false, nil, nil
}

// handleProviderToolInProgress ensures a provider/MCP tool entry exists and emits input delta.
func (oc *AIClient) handleProviderToolInProgress(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	meta *PortalMetadata,
	activeTools map[string]*activeToolCall,
	itemID string,
	toolName string,
	toolType ToolType,
) {
	tool := oc.ensureActiveToolCall(ctx, portal, state, meta, activeTools, itemID, toolName, toolType, "")
	oc.toolLifecycle(portal, state).appendInputDelta(ctx, tool, tool.toolName, "", true)
}

// handleProviderToolCompleted finalizes a provider/MCP tool with a success or failure result.
func (oc *AIClient) handleProviderToolCompleted(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	activeTools map[string]*activeToolCall,
	itemID string,
	toolName string,
	toolType ToolType,
	failureText string,
) {
	// Look up or lazily create the tool. We pass nil meta because
	// ensureActiveToolCall only uses meta for ghost display-name, which
	// handleProviderToolInProgress already handled on the in_progress event.
	// When the in_progress event was missed the tool gets startedAtMs=now
	// (acceptable approximation).
	tool := oc.ensureActiveToolCall(ctx, portal, state, nil, activeTools, itemID, toolName, toolType, "")
	if state != nil && state.ui.UIToolOutputFinalized[tool.callID] {
		return
	}

	lifecycle := oc.toolLifecycle(portal, state)
	if failureText != "" {
		lifecycle.fail(ctx, tool, true, ResultStatusError, failureText, nil)
		return
	}

	output := map[string]any{"status": "completed"}
	lifecycle.succeed(ctx, tool, true, output, output, nil)
}

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
	return oc.runStreamingTurn(ctx, log, evt, portal, meta, messages, func(prep streamingRunPrep, pruned []openai.ChatCompletionMessageParamUnion) streamingTurnAdapter {
		base := newStreamingAdapterBase(oc, log, portal, meta, prep, pruned)
		return &responsesTurnAdapter{
			streamingAdapterBase: base,
			rsc: &responseStreamContext{
				base:        &base,
				activeTools: make(map[string]*activeToolCall),
			},
		}
	})
}
