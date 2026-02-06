package connector

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
	"github.com/openai/openai-go/v3/shared/constant"
	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/ai-bridge/pkg/agents"
	"github.com/beeper/ai-bridge/pkg/agents/tools"
)

// streamingState tracks the state of a streaming response
type streamingState struct {
	turnID         string
	agentID        string
	startedAtMs    int64
	firstTokenAtMs int64
	completedAtMs  int64
	roomID         id.RoomID

	promptTokens     int64
	completionTokens int64
	reasoningTokens  int64
	totalTokens      int64

	baseInput              responses.ResponseInputParam
	accumulated            strings.Builder
	visibleAccumulated     strings.Builder
	reasoning              strings.Builder
	toolCalls              []ToolCallMetadata
	pendingImages          []generatedImage
	pendingFunctionOutputs []functionCallOutput // Function outputs to send back to API for continuation
	sourceCitations        []sourceCitation
	sourceDocuments        []sourceDocument
	generatedFiles         []generatedFilePart
	initialEventID         id.EventID
	finishReason           string
	responseID             string
	sequenceNum            int
	firstToken             bool
	statusSent             bool
	statusSentIDs          map[id.EventID]bool

	// Directive processing
	sourceEventID    id.EventID // The triggering user message event ID (for [[reply_to_current]])
	senderID         string     // The triggering sender ID (for owner-only tool gating)
	replyTarget      ReplyTarget
	replyAccumulator *streamingDirectiveAccumulator

	// Heartbeat handling
	heartbeat         *HeartbeatRunConfig
	heartbeatResultCh chan HeartbeatRunOutcome
	suppressSave      bool
	suppressSend      bool

	// AI SDK UIMessage stream tracking
	uiStarted     bool
	uiFinished    bool
	uiTextID      string
	uiReasoningID string
	uiStepOpen    bool
	uiStepCount   int
	uiToolStarted map[string]bool

	// Avoid logging repeated missing-ephemeral warnings.
	streamEphemeralUnsupported bool
}

// newStreamingState creates a new streaming state with initialized fields
func newStreamingState(ctx context.Context, meta *PortalMetadata, sourceEventID id.EventID, senderID string, roomID id.RoomID) *streamingState {
	agentID := ""
	if meta != nil {
		agentID = meta.DefaultAgentID
	}
	state := &streamingState{
		turnID:           NewTurnID(),
		agentID:          agentID,
		startedAtMs:      time.Now().UnixMilli(),
		firstToken:       true,
		sourceEventID:    sourceEventID,
		senderID:         senderID,
		roomID:           roomID,
		statusSentIDs:    make(map[id.EventID]bool),
		replyAccumulator: newStreamingDirectiveAccumulator(),
		uiToolStarted:    make(map[string]bool),
	}
	if meta != nil && normalizeSendPolicyMode(meta.SendPolicy) == "deny" {
		state.suppressSend = true
	}
	if hb := heartbeatRunFromContext(ctx); hb != nil {
		state.heartbeat = hb.Config
		state.heartbeatResultCh = hb.ResultCh
		if hb.Config != nil && hb.Config.SuppressSave {
			state.suppressSave = true
		}
		if hb.Config != nil && hb.Config.SuppressSend {
			state.suppressSend = true
		}
	}
	return state
}

func (oc *AIClient) applyStreamingReplyTarget(state *streamingState, parsed *streamingDirectiveResult) {
	if oc == nil || state == nil || parsed == nil || !parsed.HasReplyTag {
		return
	}
	if oc.resolveMatrixReplyToMode() == "off" {
		return
	}
	if parsed.ReplyToExplicitID != "" {
		state.replyTarget.ReplyTo = id.EventID(parsed.ReplyToExplicitID)
		return
	}
	if parsed.ReplyToCurrent && state.sourceEventID != "" {
		state.replyTarget.ReplyTo = state.sourceEventID
	}
}

func (oc *AIClient) markMessageSendSuccess(ctx context.Context, portal *bridgev2.Portal, evt *event.Event, state *streamingState) {
	if state == nil || state.suppressSend {
		return
	}
	if queueAcceptedStatusFromContext(ctx) {
		return
	}
	if state.statusSentIDs == nil {
		state.statusSentIDs = make(map[id.EventID]bool)
	}
	events := make([]*event.Event, 0, 4)
	if evt != nil {
		events = append(events, evt)
	}
	events = append(events, statusEventsFromContext(ctx)...)
	if portal != nil && portal.MXID != "" {
		events = append(events, oc.roomRunStatusEvents(portal.MXID)...)
	}
	for _, extra := range events {
		if extra == nil || extra.ID == "" {
			continue
		}
		if state.statusSentIDs[extra.ID] {
			continue
		}
		oc.sendSuccessStatus(ctx, portal, extra)
		state.statusSentIDs[extra.ID] = true
	}
	if len(state.statusSentIDs) > 0 {
		state.statusSent = true
	}
}

// generatedImage tracks a pending image from image generation
type generatedImage struct {
	itemID   string
	imageB64 string
	turnID   string
}

type generatedFilePart struct {
	url       string
	mediaType string
}

// functionCallOutput tracks a completed function call output for API continuation
type functionCallOutput struct {
	callID    string // The ItemID from the stream event (used as call_id in continuation)
	name      string // Tool name (for stateless continuations)
	arguments string // Raw arguments JSON (for stateless continuations)
	output    string // The result from executing the tool
}

func buildFunctionCallOutputItem(callID, output string, includeID bool) responses.ResponseInputItemUnionParam {
	item := responses.ResponseInputItemFunctionCallOutputParam{
		CallID: callID,
		Output: responses.ResponseInputItemFunctionCallOutputOutputUnionParam{
			OfString: param.NewOpt(output),
		},
	}
	if includeID {
		item.ID = param.NewOpt("fc_output_" + callID)
	}
	return responses.ResponseInputItemUnionParam{OfFunctionCallOutput: &item}
}

func recordGeneratedFile(state *streamingState, url, mediaType string) {
	if state == nil {
		return
	}
	url = strings.TrimSpace(url)
	if url == "" {
		return
	}
	for _, file := range state.generatedFiles {
		if file.url == url {
			return
		}
	}
	state.generatedFiles = append(state.generatedFiles, generatedFilePart{
		url:       url,
		mediaType: strings.TrimSpace(mediaType),
	})
}

func (oc *AIClient) buildUIMessageMetadata(state *streamingState, meta *PortalMetadata, includeUsage bool) map[string]any {
	metadata := map[string]any{
		"model":    oc.effectiveModel(meta),
		"turn_id":  state.turnID,
		"agent_id": state.agentID,
	}
	if includeUsage {
		metadata["usage"] = map[string]any{
			"prompt_tokens":     state.promptTokens,
			"completion_tokens": state.completionTokens,
			"reasoning_tokens":  state.reasoningTokens,
			"total_tokens":      state.totalTokens,
		}
		metadata["timing"] = map[string]any{
			"started_at":     state.startedAtMs,
			"first_token_at": state.firstTokenAtMs,
			"completed_at":   state.completedAtMs,
		}
		metadata["finish_reason"] = mapFinishReason(state.finishReason)
	}
	return metadata
}

func mapFinishReason(reason string) string {
	switch reason {
	case "stop":
		return "stop"
	case "end_turn", "end-turn":
		return "stop"
	case "length":
		return "length"
	case "content_filter", "content-filter":
		return "content-filter"
	case "tool_calls", "tool-calls", "tool_use", "tool-use", "toolUse":
		return "tool-calls"
	case "error":
		return "error"
	default:
		return "other"
	}
}

func shouldContinueChatToolLoop(finishReason string, toolCallCount int) bool {
	if toolCallCount <= 0 {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(finishReason)) {
	case "", "tool_calls", "tool-calls", "tool_use", "tool-use", "tooluse":
		return true
	default:
		return false
	}
}

func (oc *AIClient) emitUIStart(ctx context.Context, portal *bridgev2.Portal, state *streamingState, meta *PortalMetadata) {
	if state.uiStarted {
		return
	}
	state.uiStarted = true
	oc.emitStreamEvent(ctx, portal, state, map[string]any{
		"type":            "start",
		"messageId":       state.turnID,
		"messageMetadata": oc.buildUIMessageMetadata(state, meta, false),
	})
}

func (oc *AIClient) emitUIStepStart(ctx context.Context, portal *bridgev2.Portal, state *streamingState) {
	if state.uiStepOpen {
		return
	}
	state.uiStepOpen = true
	state.uiStepCount++
	oc.emitStreamEvent(ctx, portal, state, map[string]any{
		"type": "start-step",
	})
}

func (oc *AIClient) emitUIStepFinish(ctx context.Context, portal *bridgev2.Portal, state *streamingState) {
	if !state.uiStepOpen {
		return
	}
	state.uiStepOpen = false
	oc.emitStreamEvent(ctx, portal, state, map[string]any{
		"type": "finish-step",
	})
}

func (oc *AIClient) ensureUIText(ctx context.Context, portal *bridgev2.Portal, state *streamingState) {
	if state.uiTextID != "" {
		return
	}
	state.uiTextID = fmt.Sprintf("text-%s", state.turnID)
	oc.emitStreamEvent(ctx, portal, state, map[string]any{
		"type": "text-start",
		"id":   state.uiTextID,
	})
}

func (oc *AIClient) ensureUIReasoning(ctx context.Context, portal *bridgev2.Portal, state *streamingState) {
	if state.uiReasoningID != "" {
		return
	}
	state.uiReasoningID = fmt.Sprintf("reasoning-%s", state.turnID)
	oc.emitStreamEvent(ctx, portal, state, map[string]any{
		"type": "reasoning-start",
		"id":   state.uiReasoningID,
	})
}

func (oc *AIClient) emitUITextDelta(ctx context.Context, portal *bridgev2.Portal, state *streamingState, delta string) {
	oc.ensureUIText(ctx, portal, state)
	oc.emitStreamEvent(ctx, portal, state, map[string]any{
		"type":  "text-delta",
		"id":    state.uiTextID,
		"delta": delta,
	})
}

func (oc *AIClient) emitUIReasoningDelta(ctx context.Context, portal *bridgev2.Portal, state *streamingState, delta string) {
	oc.ensureUIReasoning(ctx, portal, state)
	oc.emitStreamEvent(ctx, portal, state, map[string]any{
		"type":  "reasoning-delta",
		"id":    state.uiReasoningID,
		"delta": delta,
	})
}

func (oc *AIClient) emitUIToolInputDelta(ctx context.Context, portal *bridgev2.Portal, state *streamingState, toolCallID, toolName, delta string, providerExecuted bool) {
	if toolCallID == "" {
		return
	}
	if !state.uiToolStarted[toolCallID] {
		state.uiToolStarted[toolCallID] = true
		oc.emitStreamEvent(ctx, portal, state, map[string]any{
			"type":             "tool-input-start",
			"toolCallId":       toolCallID,
			"toolName":         toolName,
			"providerExecuted": providerExecuted,
		})
	}
	if delta != "" {
		oc.emitStreamEvent(ctx, portal, state, map[string]any{
			"type":           "tool-input-delta",
			"toolCallId":     toolCallID,
			"inputTextDelta": delta,
		})
	}
}

func (oc *AIClient) emitUIToolInputAvailable(ctx context.Context, portal *bridgev2.Portal, state *streamingState, toolCallID, toolName string, input any, providerExecuted bool) {
	if toolCallID == "" {
		return
	}
	oc.emitStreamEvent(ctx, portal, state, map[string]any{
		"type":             "tool-input-available",
		"toolCallId":       toolCallID,
		"toolName":         toolName,
		"input":            input,
		"providerExecuted": providerExecuted,
	})
}

func (oc *AIClient) emitUIToolOutputAvailable(ctx context.Context, portal *bridgev2.Portal, state *streamingState, toolCallID string, output any, providerExecuted bool, preliminary bool) {
	if toolCallID == "" {
		return
	}
	part := map[string]any{
		"type":             "tool-output-available",
		"toolCallId":       toolCallID,
		"output":           output,
		"providerExecuted": providerExecuted,
	}
	if preliminary {
		part["preliminary"] = true
	}
	oc.emitStreamEvent(ctx, portal, state, part)
}

func (oc *AIClient) emitUIToolOutputError(ctx context.Context, portal *bridgev2.Portal, state *streamingState, toolCallID, errorText string, providerExecuted bool) {
	if toolCallID == "" {
		return
	}
	oc.emitStreamEvent(ctx, portal, state, map[string]any{
		"type":             "tool-output-error",
		"toolCallId":       toolCallID,
		"errorText":        errorText,
		"providerExecuted": providerExecuted,
	})
}

func collectToolOutputCitations(state *streamingState, toolName, output string) {
	if state == nil {
		return
	}
	citations := extractWebSearchCitationsFromToolOutput(toolName, output)
	if len(citations) == 0 {
		return
	}
	state.sourceCitations = mergeSourceCitations(state.sourceCitations, citations)
}

func (oc *AIClient) emitUIError(ctx context.Context, portal *bridgev2.Portal, state *streamingState, errText string) {
	if errText == "" {
		errText = "Unknown error"
	}
	oc.emitStreamEvent(ctx, portal, state, map[string]any{
		"type":      "error",
		"errorText": errText,
	})
}

func (oc *AIClient) emitUIFinish(ctx context.Context, portal *bridgev2.Portal, state *streamingState, meta *PortalMetadata) {
	if state.uiFinished {
		return
	}
	state.uiFinished = true
	if state.uiTextID != "" {
		oc.emitStreamEvent(ctx, portal, state, map[string]any{
			"type": "text-end",
			"id":   state.uiTextID,
		})
		state.uiTextID = ""
	}
	if state.uiReasoningID != "" {
		oc.emitStreamEvent(ctx, portal, state, map[string]any{
			"type": "reasoning-end",
			"id":   state.uiReasoningID,
		})
		state.uiReasoningID = ""
	}
	oc.emitUIStepFinish(ctx, portal, state)
	oc.emitStreamEvent(ctx, portal, state, map[string]any{
		"type":            "finish",
		"finishReason":    mapFinishReason(state.finishReason),
		"messageMetadata": oc.buildUIMessageMetadata(state, meta, true),
	})
}

// saveAssistantMessage saves the completed assistant message to the database
func (oc *AIClient) saveAssistantMessage(
	ctx context.Context,
	log zerolog.Logger,
	portal *bridgev2.Portal,
	state *streamingState,
	meta *PortalMetadata,
) {
	modelID := oc.effectiveModel(meta)
	assistantMsg := &database.Message{
		ID:        MakeMessageID(state.initialEventID),
		Room:      portal.PortalKey,
		SenderID:  modelUserID(modelID),
		MXID:      state.initialEventID,
		Timestamp: time.Now(),
		Metadata: &MessageMetadata{
			Role:           "assistant",
			Body:           state.accumulated.String(),
			CompletionID:   state.responseID,
			FinishReason:   state.finishReason,
			Model:          modelID,
			TurnID:         state.turnID,
			AgentID:        state.agentID,
			ToolCalls:      state.toolCalls,
			StartedAtMs:    state.startedAtMs,
			FirstTokenAtMs: state.firstTokenAtMs,
			CompletedAtMs:  state.completedAtMs,
			HasToolCalls:   len(state.toolCalls) > 0,
			// Reasoning fields (only populated by Responses API)
			ThinkingContent:    state.reasoning.String(),
			ThinkingTokenCount: len(strings.Fields(state.reasoning.String())),
			PromptTokens:       state.promptTokens,
			CompletionTokens:   state.completionTokens,
			ReasoningTokens:    state.reasoningTokens,
		},
	}
	if err := oc.UserLogin.Bridge.DB.Message.Insert(ctx, assistantMsg); err != nil {
		log.Warn().Err(err).Msg("Failed to save assistant message to database")
	} else {
		log.Debug().Str("msg_id", string(assistantMsg.ID)).Msg("Saved assistant message to database")
	}
	oc.notifySessionMemoryChange(ctx, portal, meta, false)

	// Save LastResponseID for "responses" mode context chaining (OpenAI-only)
	if meta.ConversationMode == "responses" && state.responseID != "" && !oc.isOpenRouterProvider() {
		meta.LastResponseID = state.responseID
		if err := portal.Save(ctx); err != nil {
			log.Warn().Err(err).Msg("Failed to save portal after storing response ID")
		}
	}
}

// buildResponsesAPIParams creates common Responses API parameters for both streaming and non-streaming paths
func (oc *AIClient) buildResponsesAPIParams(ctx context.Context, portal *bridgev2.Portal, meta *PortalMetadata, messages []openai.ChatCompletionMessageParamUnion) responses.ResponseNewParams {
	log := zerolog.Ctx(ctx)

	params := responses.ResponseNewParams{
		Model:           shared.ResponsesModel(oc.effectiveModelForAPI(meta)),
		MaxOutputTokens: openai.Int(int64(oc.effectiveMaxTokens(meta))),
	}

	systemPrompt := oc.effectivePrompt(meta)

	// Use previous_response_id if in "responses" mode and ID exists.
	// OpenRouter's Responses API is stateless, so always send full history there.
	usePreviousResponse := meta.ConversationMode == "responses" && meta.LastResponseID != "" && !oc.isOpenRouterProvider()
	if usePreviousResponse {
		params.PreviousResponseID = openai.String(meta.LastResponseID)
		if systemPrompt != "" {
			params.Instructions = openai.String(systemPrompt)
		}
		// Still need to pass the latest user message as input
		if len(messages) > 0 {
			latestMsg := messages[len(messages)-1]
			input := oc.convertToResponsesInput([]openai.ChatCompletionMessageParamUnion{latestMsg}, meta)
			params.Input = responses.ResponseNewParamsInputUnion{
				OfInputItemList: input,
			}
		}
		log.Debug().Str("previous_response_id", meta.LastResponseID).Msg("Using previous_response_id for context")
	} else {
		// Build full message history
		input := oc.convertToResponsesInput(messages, meta)
		params.Input = responses.ResponseNewParamsInputUnion{
			OfInputItemList: input,
		}
	}

	// Add reasoning effort if configured (uses inheritance: room → user → default)
	if reasoningEffort := oc.effectiveReasoningEffort(meta); reasoningEffort != "" {
		params.Reasoning = shared.ReasoningParam{
			Effort: shared.ReasoningEffort(reasoningEffort),
		}
	}

	// OpenRouter's Responses API only supports function-type tools.
	isOpenRouter := oc.isOpenRouterProvider()
	log.Debug().
		Bool("is_openrouter", isOpenRouter).
		Str("detected_provider", loginMetadata(oc.UserLogin).Provider).
		Msg("Provider detection for tool filtering")

	// Add builtin function tools if model supports tool calling
	if meta.Capabilities.SupportsToolCalling {
		enabledTools := GetEnabledBuiltinTools(func(name string) bool {
			return oc.isToolEnabled(meta, name)
		})
		if len(enabledTools) > 0 {
			strictMode := resolveToolStrictMode(oc.isOpenRouterProvider())
			params.Tools = append(params.Tools, ToOpenAITools(enabledTools, strictMode, &oc.log)...)
			log.Debug().Int("count", len(enabledTools)).Msg("Added builtin function tools")
		}

		// Add session tools for non-boss rooms
		if !hasBossAgent(meta) && !oc.isBuilderRoom(portal) {
			var enabledSessions []*tools.Tool
			for _, tool := range tools.SessionTools() {
				if oc.isToolEnabled(meta, tool.Name) {
					enabledSessions = append(enabledSessions, tool)
				}
			}
			if len(enabledSessions) > 0 {
				strictMode := resolveToolStrictMode(oc.isOpenRouterProvider())
				params.Tools = append(params.Tools, bossToolsToOpenAI(enabledSessions, strictMode, &oc.log)...)
				log.Debug().Int("count", len(enabledSessions)).Msg("Added session tools")
			}
		}
	}

	// Add boss tools if this is a Boss room
	if hasBossAgent(meta) || oc.isBuilderRoom(portal) {
		var enabledBoss []*tools.Tool
		for _, tool := range tools.BossTools() {
			if oc.isToolEnabled(meta, tool.Name) {
				enabledBoss = append(enabledBoss, tool)
			}
		}
		strictMode := resolveToolStrictMode(oc.isOpenRouterProvider())
		params.Tools = append(params.Tools, bossToolsToOpenAI(enabledBoss, strictMode, &oc.log)...)
		log.Debug().Int("count", len(enabledBoss)).Msg("Added boss agent tools")
	}

	if oc.isOpenRouterProvider() {
		params.Tools = renameWebSearchToolParams(params.Tools)
	}

	// Prevent duplicate tool names (Anthropic rejects duplicates)
	params.Tools = dedupeToolParams(params.Tools)
	logToolParamDuplicates(log, params.Tools)

	return params
}

// bossToolsToOpenAI converts boss tools to OpenAI Responses API format.
func bossToolsToOpenAI(bossTools []*tools.Tool, strictMode ToolStrictMode, log *zerolog.Logger) []responses.ToolUnionParam {
	var result []responses.ToolUnionParam
	for _, t := range bossTools {
		var schema map[string]any
		switch v := t.InputSchema.(type) {
		case nil:
			schema = nil
		case map[string]any:
			schema = v
		default:
			encoded, err := json.Marshal(v)
			if err == nil {
				if err := json.Unmarshal(encoded, &schema); err != nil {
					schema = nil
				}
			}
		}
		if schema != nil {
			var stripped []string
			schema, stripped = sanitizeToolSchemaWithReport(schema)
			logSchemaSanitization(log, t.Name, stripped)
		}
		strict := shouldUseStrictMode(strictMode, schema)
		toolParam := responses.ToolUnionParam{
			OfFunction: &responses.FunctionToolParam{
				Name:       t.Name,
				Parameters: schema,
				Strict:     param.NewOpt(strict),
				Type:       constant.ValueOf[constant.Function](),
			},
		}
		if t.Description != "" && toolParam.OfFunction != nil {
			toolParam.OfFunction.Description = openai.String(t.Description)
		}
		result = append(result, toolParam)
	}
	return result
}

// bossToolsToChatTools converts boss tools to OpenAI Chat Completions tool format.
func bossToolsToChatTools(bossTools []*tools.Tool, log *zerolog.Logger) []openai.ChatCompletionToolUnionParam {
	var result []openai.ChatCompletionToolUnionParam
	for _, t := range bossTools {
		var schema map[string]any
		switch v := t.InputSchema.(type) {
		case nil:
			schema = nil
		case map[string]any:
			schema = v
		default:
			encoded, err := json.Marshal(v)
			if err == nil {
				if err := json.Unmarshal(encoded, &schema); err != nil {
					schema = nil
				}
			}
		}
		if schema != nil {
			var stripped []string
			schema, stripped = sanitizeToolSchemaWithReport(schema)
			logSchemaSanitization(log, t.Name, stripped)
		}
		function := openai.FunctionDefinitionParam{
			Name:       t.Name,
			Parameters: schema,
		}
		if t.Description != "" {
			function.Description = openai.String(t.Description)
		}
		result = append(result, openai.ChatCompletionToolUnionParam{
			OfFunction: &openai.ChatCompletionFunctionToolParam{
				Function: function,
				Type:     constant.ValueOf[constant.Function](),
			},
		})
	}
	return result
}

// streamingResponseWithToolSchemaFallback retries via Chat Completions when the provider
// rejects tool schemas in the Responses API.
func (oc *AIClient) streamingResponseWithToolSchemaFallback(
	ctx context.Context,
	evt *event.Event,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	messages []openai.ChatCompletionMessageParamUnion,
) (bool, *ContextLengthError, error) {
	success, cle, err := oc.streamingResponse(ctx, evt, portal, meta, messages)
	if success || cle != nil || err == nil {
		return success, cle, err
	}
	if IsToolUniquenessError(err) {
		oc.log.Warn().Err(err).Msg("Duplicate tool names rejected; retrying with chat completions")
		success, cle, chatErr := oc.streamChatCompletions(ctx, evt, portal, meta, messages)
		if success || cle != nil || chatErr == nil {
			return success, cle, chatErr
		}
		if IsToolSchemaError(chatErr) || IsToolUniquenessError(chatErr) {
			oc.log.Warn().Err(chatErr).Msg("Chat completions tools rejected; retrying without tools")
			if meta != nil {
				metaCopy := *meta
				metaCopy.Capabilities = meta.Capabilities
				metaCopy.Capabilities.SupportsToolCalling = false
				return oc.streamChatCompletions(ctx, evt, portal, &metaCopy, messages)
			}
		}
		return success, cle, chatErr
	}
	if IsToolSchemaError(err) {
		oc.log.Warn().Err(err).Msg("Responses tool schema rejected; falling back to chat completions")
		success, cle, chatErr := oc.streamChatCompletions(ctx, evt, portal, meta, messages)
		if success || cle != nil || chatErr == nil {
			return success, cle, chatErr
		}
		if IsToolSchemaError(chatErr) {
			oc.log.Warn().Err(chatErr).Msg("Chat completions tool schema rejected; retrying without tools")
			if meta != nil {
				metaCopy := *meta
				metaCopy.Capabilities = meta.Capabilities
				metaCopy.Capabilities.SupportsToolCalling = false
				return oc.streamChatCompletions(ctx, evt, portal, &metaCopy, messages)
			}
		}
		return success, cle, chatErr
	}
	if IsNoResponseChunksError(err) {
		oc.log.Warn().Err(err).Msg("Responses streaming returned no chunks; retrying without tools")
		if meta != nil && meta.Capabilities.SupportsToolCalling {
			metaCopy := *meta
			metaCopy.Capabilities = meta.Capabilities
			metaCopy.Capabilities.SupportsToolCalling = false
			success, cle, retryErr := oc.streamingResponse(ctx, evt, portal, &metaCopy, messages)
			if success || cle != nil || retryErr == nil {
				return success, cle, retryErr
			}
			err = retryErr
		}
		oc.log.Warn().Err(err).Msg("Responses retry failed; falling back to chat completions")
		return oc.streamChatCompletions(ctx, evt, portal, meta, messages)
	}
	return success, cle, err
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
	maxToolRounds := 3

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
		initErr := fmt.Errorf("responses streaming not available")
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
		case "response.output_text.delta":
			touchTyping()
			state.accumulated.WriteString(streamEvent.Delta)
			parsed := (*streamingDirectiveResult)(nil)
			if state.replyAccumulator != nil {
				parsed = state.replyAccumulator.Consume(streamEvent.Delta, false)
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

		case "response.refusal.delta":
			touchTyping()
			if typingSignals != nil {
				typingSignals.SignalTextDelta(streamEvent.Delta)
			}
			oc.emitUITextDelta(ctx, portal, state, streamEvent.Delta)

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
			argsJSON := strings.TrimSpace(tool.input.String())
			if argsJSON == "" {
				argsJSON = strings.TrimSpace(streamEvent.Arguments)
			}
			argsJSON = normalizeToolArgsJSON(argsJSON)

			var inputMap any
			if err := json.Unmarshal([]byte(argsJSON), &inputMap); err != nil {
				inputMap = argsJSON
			}
			oc.emitUIToolInputAvailable(ctx, portal, state, tool.callID, toolName, inputMap, tool.toolType == ToolTypeProvider)

			resultStatus := ResultStatusSuccess
			var result string
			if !oc.isToolEnabled(meta, toolName) {
				resultStatus = ResultStatusError
				result = fmt.Sprintf("Error: tool %s is disabled", toolName)
			} else {
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
						displayResult = "Audio message sent successfully"
					}
				}
				result = displayResult
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
					for _, imageB64 := range images {
						imageData, mimeType, err := decodeBase64Image(imageB64)
						if err != nil {
							log.Warn().Err(err).Msg("Failed to decode generated image")
							continue
						}
						_, mediaURL, err := oc.sendGeneratedImage(ctx, portal, imageData, mimeType, state.turnID)
						if err != nil {
							log.Warn().Err(err).Msg("Failed to send generated image")
							continue
						}
						recordGeneratedFile(state, mediaURL, mimeType)
						success++
					}
					if success == len(images) && success > 0 {
						displayResult = fmt.Sprintf("Images generated and sent successfully (%d)", success)
					} else if success > 0 {
						displayResult = fmt.Sprintf("Images generated with %d/%d sent successfully", success, len(images))
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
					if _, mediaURL, err := oc.sendGeneratedImage(ctx, portal, imageData, mimeType, state.turnID); err != nil {
						log.Warn().Err(err).Msg("Failed to send generated image")
						displayResult = "Error: failed to send generated image"
						resultStatus = ResultStatusError
					} else {
						recordGeneratedFile(state, mediaURL, mimeType)
						displayResult = "Image generated and sent successfully"
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

			// Normalize input for storage
			inputMapForMeta := map[string]any{}
			if parsed, ok := inputMap.(map[string]any); ok {
				inputMapForMeta = parsed
			} else if raw, ok := inputMap.(string); ok && raw != "" {
				inputMapForMeta = map[string]any{"_raw": raw}
			}

			// Track tool call in metadata
			state.toolCalls = append(state.toolCalls, ToolCallMetadata{
				CallID:        tool.callID,
				ToolName:      toolName,
				ToolType:      string(tool.toolType),
				Input:         inputMapForMeta,
				Output:        map[string]any{"result": result},
				Status:        string(ToolStatusCompleted),
				ResultStatus:  string(resultStatus),
				StartedAtMs:   tool.startedAtMs,
				CompletedAtMs: time.Now().UnixMilli(),
			})

			if resultStatus == ResultStatusSuccess {
				oc.emitUIToolOutputAvailable(ctx, portal, state, tool.callID, result, tool.toolType == ToolTypeProvider, false)
			} else {
				oc.emitUIToolOutputError(ctx, portal, state, tool.callID, result, tool.toolType == ToolTypeProvider)
			}

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
				state.toolCalls = append(state.toolCalls, ToolCallMetadata{
					CallID:        callID,
					ToolName:      "web_search",
					ToolType:      string(tool.toolType),
					Status:        string(ToolStatusCompleted),
					ResultStatus:  string(ResultStatusSuccess),
					StartedAtMs:   tool.startedAtMs,
					CompletedAtMs: time.Now().UnixMilli(),
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
				state.toolCalls = append(state.toolCalls, ToolCallMetadata{
					CallID:        callID,
					ToolName:      "image_generation",
					ToolType:      string(tool.toolType),
					Status:        string(ToolStatusCompleted),
					ResultStatus:  string(ResultStatusSuccess),
					StartedAtMs:   tool.startedAtMs,
					CompletedAtMs: time.Now().UnixMilli(),
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
				state.sourceCitations = append(state.sourceCitations, citation)
			}
			if document, ok := extractDocumentCitation(streamEvent.Annotation); ok {
				state.sourceDocuments = append(state.sourceDocuments, document)
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

	// If there are pending function outputs, send them back to the API for continuation
	// This loop continues until the model generates a response without tool calls
	continuationRound := 0
	for len(state.pendingFunctionOutputs) > 0 && state.responseID != "" {
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
			Str("previous_response_id", state.responseID).
			Msg("Continuing response with function call outputs")

		// Build continuation request with function call outputs
		continuationParams := oc.buildContinuationParams(state, meta)
		pendingOutputs := append([]functionCallOutput(nil), state.pendingFunctionOutputs...)

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
		}

		// Reset active tools for new iteration
		activeTools = make(map[string]*activeToolCall)

		// Start continuation stream
		stream = oc.api.Responses.NewStreaming(ctx, continuationParams)
		if stream == nil {
			initErr := fmt.Errorf("continuation streaming not available")
			logResponsesFailure(log, initErr, continuationParams, meta, messages, "continuation_init")
			state.finishReason = "error"
			oc.emitUIError(ctx, portal, state, initErr.Error())
			oc.emitUIFinish(ctx, portal, state, meta)
			if state.initialEventID != "" {
				return false, nil, &NonFallbackError{Err: initErr}
			}
			return false, nil, &PreDeltaError{Err: initErr}
		}
		// Clear pending outputs only once continuation stream has actually started.
		state.pendingFunctionOutputs = nil
		oc.emitUIStepStart(ctx, portal, state)

		// Process continuation stream events
		for stream.Next() {
			streamEvent := stream.Current()

			switch streamEvent.Type {
			case "response.output_text.delta":
				touchTyping()
				state.accumulated.WriteString(streamEvent.Delta)
				parsed := (*streamingDirectiveResult)(nil)
				if state.replyAccumulator != nil {
					parsed = state.replyAccumulator.Consume(streamEvent.Delta, false)
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

			case "response.refusal.delta":
				touchTyping()
				if typingSignals != nil {
					typingSignals.SignalTextDelta(streamEvent.Delta)
				}
				oc.emitUITextDelta(ctx, portal, state, streamEvent.Delta)

			case "response.output_text.annotation.added":
				if citation, ok := extractURLCitation(streamEvent.Annotation); ok {
					state.sourceCitations = append(state.sourceCitations, citation)
				}
				if document, ok := extractDocumentCitation(streamEvent.Annotation); ok {
					state.sourceDocuments = append(state.sourceDocuments, document)
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
				argsJSON := strings.TrimSpace(tool.input.String())
				if argsJSON == "" {
					argsJSON = strings.TrimSpace(streamEvent.Arguments)
				}
				argsJSON = normalizeToolArgsJSON(argsJSON)
				var inputMap any
				if err := json.Unmarshal([]byte(argsJSON), &inputMap); err != nil {
					inputMap = argsJSON
				}
				oc.emitUIToolInputAvailable(ctx, portal, state, tool.callID, toolName, inputMap, tool.toolType == ToolTypeProvider)

				resultStatus := ResultStatusSuccess
				var result string
				if !oc.isToolEnabled(meta, toolName) {
					resultStatus = ResultStatusError
					result = fmt.Sprintf("Error: tool %s is disabled", toolName)
				} else {
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
							displayResult = "Audio message sent successfully"
						}
					}
					result = displayResult
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
						for _, imageB64 := range images {
							imageData, mimeType, err := decodeBase64Image(imageB64)
							if err != nil {
								log.Warn().Err(err).Msg("Failed to decode generated image (continuation)")
								continue
							}
							_, mediaURL, err := oc.sendGeneratedImage(ctx, portal, imageData, mimeType, state.turnID)
							if err != nil {
								log.Warn().Err(err).Msg("Failed to send generated image (continuation)")
								continue
							}
							recordGeneratedFile(state, mediaURL, mimeType)
							success++
						}
						if success == len(images) && success > 0 {
							displayResult = fmt.Sprintf("Images generated and sent successfully (%d)", success)
						} else if success > 0 {
							displayResult = fmt.Sprintf("Images generated with %d/%d sent successfully", success, len(images))
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
						if _, mediaURL, err := oc.sendGeneratedImage(ctx, portal, imageData, mimeType, state.turnID); err != nil {
							log.Warn().Err(err).Msg("Failed to send generated image (continuation)")
							displayResult = "Error: failed to send generated image"
							resultStatus = ResultStatusError
						} else {
							recordGeneratedFile(state, mediaURL, mimeType)
							displayResult = "Image generated and sent successfully"
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

				inputMapForMeta := map[string]any{}
				if parsed, ok := inputMap.(map[string]any); ok {
					inputMapForMeta = parsed
				} else if raw, ok := inputMap.(string); ok && raw != "" {
					inputMapForMeta = map[string]any{"_raw": raw}
				}

				state.toolCalls = append(state.toolCalls, ToolCallMetadata{
					CallID:        tool.callID,
					ToolName:      toolName,
					ToolType:      string(tool.toolType),
					Input:         inputMapForMeta,
					Output:        map[string]any{"result": result},
					Status:        string(ToolStatusCompleted),
					ResultStatus:  string(resultStatus),
					StartedAtMs:   tool.startedAtMs,
					CompletedAtMs: time.Now().UnixMilli(),
				})

				if resultStatus == ResultStatusSuccess {
					oc.emitUIToolOutputAvailable(ctx, portal, state, tool.callID, result, tool.toolType == ToolTypeProvider, false)
				} else {
					oc.emitUIToolOutputError(ctx, portal, state, tool.callID, result, tool.toolType == ToolTypeProvider)
				}

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
	oc.emitUIFinish(ctx, portal, state, meta)

	// Send any generated images as separate messages
	for _, img := range state.pendingImages {
		imageData, mimeType, err := decodeBase64Image(img.imageB64)
		if err != nil {
			log.Warn().Err(err).Str("item_id", img.itemID).Msg("Failed to decode generated image")
			continue
		}
		eventID, mediaURL, err := oc.sendGeneratedImage(ctx, portal, imageData, mimeType, img.turnID)
		if err != nil {
			log.Warn().Err(err).Str("item_id", img.itemID).Msg("Failed to send generated image to Matrix")
			continue
		}
		recordGeneratedFile(state, mediaURL, mimeType)
		log.Info().Stringer("event_id", eventID).Str("item_id", img.itemID).Msg("Sent generated image to Matrix")
	}

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

	return true, nil, nil
}

// buildContinuationParams builds params for continuing a response after tool execution
func (oc *AIClient) buildContinuationParams(state *streamingState, meta *PortalMetadata) responses.ResponseNewParams {
	params := responses.ResponseNewParams{
		Model:           shared.ResponsesModel(oc.effectiveModelForAPI(meta)),
		MaxOutputTokens: openai.Int(int64(oc.effectiveMaxTokens(meta))),
	}

	if systemPrompt := oc.effectivePrompt(meta); systemPrompt != "" {
		params.Instructions = openai.String(systemPrompt)
	}

	isOpenRouter := oc.isOpenRouterProvider()
	if !isOpenRouter {
		params.PreviousResponseID = openai.String(state.responseID)
	}

	// Build function call outputs as input
	var input responses.ResponseInputParam
	if isOpenRouter && len(state.baseInput) > 0 {
		// OpenRouter Responses API is stateless: include full history plus tool calls.
		input = append(input, state.baseInput...)
	}
	for _, output := range state.pendingFunctionOutputs {
		if isOpenRouter && output.name != "" {
			args := output.arguments
			if strings.TrimSpace(args) == "" {
				args = "{}"
			}
			input = append(input, responses.ResponseInputItemParamOfFunctionCall(args, output.callID, output.name))
		}
		input = append(input, buildFunctionCallOutputItem(output.callID, output.output, isOpenRouter))
	}
	steerItems := oc.drainSteerQueue(state.roomID)
	if len(steerItems) > 0 {
		steerInput := oc.buildSteerInputItems(steerItems, meta)
		if len(steerInput) > 0 {
			input = append(input, steerInput...)
			if isOpenRouter && len(state.baseInput) > 0 {
				state.baseInput = append(state.baseInput, steerInput...)
			}
		}
	}
	params.Input = responses.ResponseNewParamsInputUnion{
		OfInputItemList: input,
	}

	// Add reasoning effort if configured
	if reasoningEffort := oc.effectiveReasoningEffort(meta); reasoningEffort != "" {
		params.Reasoning = shared.ReasoningParam{
			Effort: shared.ReasoningEffort(reasoningEffort),
		}
	}

	// OpenRouter's Responses API only supports function-type tools.
	if meta.Capabilities.SupportsToolCalling {
		enabledTools := GetEnabledBuiltinTools(func(name string) bool {
			return oc.isToolEnabled(meta, name)
		})
		if len(enabledTools) > 0 {
			strictMode := resolveToolStrictMode(oc.isOpenRouterProvider())
			params.Tools = append(params.Tools, ToOpenAITools(enabledTools, strictMode, &oc.log)...)
		}
	}

	// Add boss tools for Boss agent rooms (needed for multi-turn tool use)
	agentID := resolveAgentID(meta)
	if hasBossAgent(meta) || agents.IsBossAgent(agentID) {
		var enabledBoss []*tools.Tool
		for _, tool := range tools.BossTools() {
			if oc.isToolEnabled(meta, tool.Name) {
				enabledBoss = append(enabledBoss, tool)
			}
		}
		strictMode := resolveToolStrictMode(oc.isOpenRouterProvider())
		params.Tools = append(params.Tools, bossToolsToOpenAI(enabledBoss, strictMode, &oc.log)...)
	}

	// Add session tools for non-boss rooms (needed for multi-turn tool use)
	if meta.Capabilities.SupportsToolCalling && !(hasBossAgent(meta) || agents.IsBossAgent(agentID)) {
		var enabledSessions []*tools.Tool
		for _, tool := range tools.SessionTools() {
			if oc.isToolEnabled(meta, tool.Name) {
				enabledSessions = append(enabledSessions, tool)
			}
		}
		if len(enabledSessions) > 0 {
			strictMode := resolveToolStrictMode(oc.isOpenRouterProvider())
			params.Tools = append(params.Tools, bossToolsToOpenAI(enabledSessions, strictMode, &oc.log)...)
		}
	}

	if oc.isOpenRouterProvider() {
		params.Tools = renameWebSearchToolParams(params.Tools)
	}

	// Prevent duplicate tool names (Anthropic rejects duplicates)
	params.Tools = dedupeToolParams(params.Tools)
	logToolParamDuplicates(&oc.log, params.Tools)

	return params
}

func (oc *AIClient) buildSteerInputItems(items []pendingQueueItem, meta *PortalMetadata) responses.ResponseInputParam {
	if oc == nil || len(items) == 0 {
		return nil
	}
	var input responses.ResponseInputParam
	for _, item := range items {
		if item.pending.Type != pendingTypeText {
			continue
		}
		prompt := strings.TrimSpace(item.prompt)
		if prompt == "" {
			prompt = item.pending.MessageBody
			if item.pending.Event != nil {
				prompt = appendMessageIDHint(prompt, item.pending.Event.ID)
			}
		}
		prompt = strings.TrimSpace(prompt)
		if prompt == "" {
			continue
		}
		messages := []openai.ChatCompletionMessageParamUnion{openai.UserMessage(prompt)}
		input = append(input, oc.convertToResponsesInput(messages, meta)...)
	}
	return input
}

// streamChatCompletions handles streaming using Chat Completions API (for audio support)
// This is used as a fallback when the prompt contains audio content, since
// SDK v3.16.0 has ResponseInputAudioParam defined but NOT wired into ResponseInputContentUnionParam.
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

	// Initialize streaming state with source event ID for [[reply_to_current]] support
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

	currentMessages := messages
	maxToolRounds := 3

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
		if meta.Capabilities.SupportsToolCalling {
			enabledTools := GetEnabledBuiltinTools(func(name string) bool {
				return oc.isToolEnabled(meta, name)
			})
			if len(enabledTools) > 0 {
				params.Tools = append(params.Tools, ToOpenAIChatTools(enabledTools, &oc.log)...)
			}
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
			initErr := fmt.Errorf("chat completions streaming not available")
			logChatCompletionsFailure(log, initErr, params, meta, currentMessages, "stream_init")
			return false, nil, &PreDeltaError{Err: initErr}
		}

		// Track active tool calls by index
		activeTools := make(map[int]*activeToolCall)
		var roundContent strings.Builder
		state.finishReason = ""

		oc.emitUIStepStart(ctx, portal, state)

		for stream.Next() {
			chunk := stream.Current()
			oc.markMessageSendSuccess(ctx, portal, evt, state)

			if chunk.Usage.TotalTokens > 0 || chunk.Usage.PromptTokens > 0 || chunk.Usage.CompletionTokens > 0 {
				state.promptTokens = chunk.Usage.PromptTokens
				state.completionTokens = chunk.Usage.CompletionTokens
				state.reasoningTokens = chunk.Usage.CompletionTokensDetails.ReasoningTokens
				state.totalTokens = chunk.Usage.TotalTokens
			}

			for _, choice := range chunk.Choices {
				if choice.Delta.Content != "" {
					touchTyping()
					state.accumulated.WriteString(choice.Delta.Content)
					roundContent.WriteString(choice.Delta.Content)

					parsed := (*streamingDirectiveResult)(nil)
					if state.replyAccumulator != nil {
						parsed = state.replyAccumulator.Consume(choice.Delta.Content, false)
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
				}

				if choice.Delta.Refusal != "" {
					touchTyping()
					if typingSignals != nil {
						typingSignals.SignalTextDelta(choice.Delta.Refusal)
					}
					oc.emitUITextDelta(ctx, portal, state, choice.Delta.Refusal)
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
					}

					// Accumulate arguments
					if toolDelta.Function.Arguments != "" {
						tool.input.WriteString(toolDelta.Function.Arguments)
						oc.emitUIToolInputDelta(ctx, portal, state, tool.callID, tool.toolName, toolDelta.Function.Arguments, false)
					}
				}

				if choice.FinishReason != "" {
					state.finishReason = string(choice.FinishReason)
				}
			}

		}

		oc.emitUIStepFinish(ctx, portal, state)

		if err := stream.Err(); err != nil {
			if cle := ParseContextLengthError(err); cle != nil {
				return false, cle, nil
			}
			logChatCompletionsFailure(log, err, params, meta, currentMessages, "stream_err")
			state.finishReason = "error"
			oc.emitUIError(ctx, portal, state, err.Error())
			oc.emitUIFinish(ctx, portal, state, meta)
			if state.initialEventID != "" {
				return false, nil, &NonFallbackError{Err: err}
			}
			return false, nil, &PreDeltaError{Err: err}
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
					var err error
					result, err = oc.executeBuiltinTool(toolCtx, portal, toolName, argsJSON)
					if err != nil {
						log.Warn().Err(err).Str("tool", toolName).Msg("Tool execution failed (Chat Completions)")
						result = fmt.Sprintf("Error: %s", err.Error())
						resultStatus = ResultStatusError
					}

					// Check for TTS audio result (AUDIO: prefix)
					if strings.HasPrefix(result, TTSResultPrefix) {
						audioB64 := strings.TrimPrefix(result, TTSResultPrefix)
						audioData, decodeErr := base64.StdEncoding.DecodeString(audioB64)
						if decodeErr != nil {
							log.Warn().Err(decodeErr).Msg("Failed to decode TTS audio (Chat Completions)")
							result = "Error: failed to decode TTS audio"
							resultStatus = ResultStatusError
						} else {
							mimeType := detectAudioMime(audioData, "audio/mpeg")
							if _, mediaURL, sendErr := oc.sendGeneratedAudio(ctx, portal, audioData, mimeType, state.turnID); sendErr != nil {
								log.Warn().Err(sendErr).Msg("Failed to send TTS audio (Chat Completions)")
								result = "Error: failed to send TTS audio"
								resultStatus = ResultStatusError
							} else {
								recordGeneratedFile(state, mediaURL, mimeType)
								result = "Audio message sent successfully"
							}
						}
					}

					// Check for image generation result (IMAGE: / IMAGES: prefix)
					if strings.HasPrefix(result, ImagesResultPrefix) {
						payload := strings.TrimPrefix(result, ImagesResultPrefix)
						var images []string
						if err := json.Unmarshal([]byte(payload), &images); err != nil {
							log.Warn().Err(err).Msg("Failed to parse generated images payload (Chat Completions)")
							result = "Error: failed to parse generated images"
							resultStatus = ResultStatusError
						} else {
							success := 0
							for _, imageB64 := range images {
								imageData, mimeType, decodeErr := decodeBase64Image(imageB64)
								if decodeErr != nil {
									log.Warn().Err(decodeErr).Msg("Failed to decode generated image (Chat Completions)")
									continue
								}
								_, mediaURL, err := oc.sendGeneratedImage(ctx, portal, imageData, mimeType, state.turnID)
								if err != nil {
									log.Warn().Err(err).Msg("Failed to send generated image (Chat Completions)")
									continue
								}
								recordGeneratedFile(state, mediaURL, mimeType)
								success++
							}
							if success == len(images) && success > 0 {
								result = fmt.Sprintf("Images generated and sent successfully (%d)", success)
							} else if success > 0 {
								result = fmt.Sprintf("Images generated with %d/%d sent successfully", success, len(images))
								resultStatus = ResultStatusError
							} else {
								result = "Error: failed to send generated images"
								resultStatus = ResultStatusError
							}
						}
					} else if strings.HasPrefix(result, ImageResultPrefix) {
						imageB64 := strings.TrimPrefix(result, ImageResultPrefix)
						imageData, mimeType, decodeErr := decodeBase64Image(imageB64)
						if decodeErr != nil {
							log.Warn().Err(decodeErr).Msg("Failed to decode generated image (Chat Completions)")
							result = "Error: failed to decode generated image"
							resultStatus = ResultStatusError
						} else {
							if _, mediaURL, sendErr := oc.sendGeneratedImage(ctx, portal, imageData, mimeType, state.turnID); sendErr != nil {
								log.Warn().Err(sendErr).Msg("Failed to send generated image (Chat Completions)")
								result = "Error: failed to send generated image"
								resultStatus = ResultStatusError
							} else {
								recordGeneratedFile(state, mediaURL, mimeType)
								result = "Image generated and sent successfully"
							}
						}
					}
				}

				// Normalize input for storage
				var inputMap any
				if err := json.Unmarshal([]byte(argsJSON), &inputMap); err != nil {
					inputMap = argsJSON
				}
				inputMapForMeta := map[string]any{}
				if parsed, ok := inputMap.(map[string]any); ok {
					inputMapForMeta = parsed
				} else if raw, ok := inputMap.(string); ok && raw != "" {
					inputMapForMeta = map[string]any{"_raw": raw}
				}
				oc.emitUIToolInputAvailable(ctx, portal, state, tool.callID, toolName, inputMap, false)

				// Track tool call in metadata
				state.toolCalls = append(state.toolCalls, ToolCallMetadata{
					CallID:        tool.callID,
					ToolName:      toolName,
					ToolType:      string(tool.toolType),
					Input:         inputMapForMeta,
					Output:        map[string]any{"result": result},
					Status:        string(ToolStatusCompleted),
					ResultStatus:  string(resultStatus),
					StartedAtMs:   tool.startedAtMs,
					CompletedAtMs: time.Now().UnixMilli(),
				})

				if resultStatus == ResultStatusSuccess {
					collectToolOutputCitations(state, toolName, result)
					oc.emitUIToolOutputAvailable(ctx, portal, state, tool.callID, result, tool.toolType == ToolTypeProvider, false)
				} else {
					oc.emitUIToolOutputError(ctx, portal, state, tool.callID, result, tool.toolType == ToolTypeProvider)
				}

				toolResults = append(toolResults, chatToolResult{callID: tool.callID, output: result})
			}
		}

		// Continue if tools were requested.
		// Some Anthropic-compatible adapters may emit `tool_use` (or omit finish reason)
		// even when tool calls are present.
		if shouldContinueChatToolLoop(state.finishReason, len(toolCallParams)) {
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
						if item.pending.Event != nil {
							prompt = appendMessageIDHint(prompt, item.pending.Event.ID)
						}
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
	oc.emitUIFinish(ctx, portal, state, meta)

	// Send final edit and save to database
	if state.initialEventID != "" {
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
	return true, nil, nil
}

// convertToResponsesInput converts Chat Completion messages to Responses API input items
// Supports native multimodal content: images (ResponseInputImageParam), files/PDFs (ResponseInputFileParam)
// Note: Audio is handled via Chat Completions API fallback (SDK v3.16.0 lacks Responses API audio union support)
func (oc *AIClient) convertToResponsesInput(messages []openai.ChatCompletionMessageParamUnion, _ *PortalMetadata) responses.ResponseInputParam {
	var input responses.ResponseInputParam

	for _, msg := range messages {
		if msg.OfTool != nil {
			toolCallID := strings.TrimSpace(msg.OfTool.ToolCallID)
			content := strings.TrimSpace(extractToolContent(msg.OfTool.Content))
			if toolCallID != "" && content != "" {
				input = append(input, responses.ResponseInputItemUnionParam{
					OfFunctionCallOutput: &responses.ResponseInputItemFunctionCallOutputParam{
						CallID: toolCallID,
						Output: responses.ResponseInputItemFunctionCallOutputOutputUnionParam{
							OfString: openai.String(content),
						},
					},
				})
			}
			continue
		}

		if msg.OfUser != nil {
			var contentParts responses.ResponseInputMessageContentListParam
			hasMultimodal := false
			textContent := ""

			if msg.OfUser.Content.OfString.Value != "" {
				textContent = msg.OfUser.Content.OfString.Value
			}

			if len(msg.OfUser.Content.OfArrayOfContentParts) > 0 {
				for _, part := range msg.OfUser.Content.OfArrayOfContentParts {
					if part.OfText != nil && part.OfText.Text != "" {
						if textContent != "" {
							textContent += "\n"
						}
						textContent += part.OfText.Text
					}
					if part.OfImageURL != nil && part.OfImageURL.ImageURL.URL != "" {
						hasMultimodal = true
						detail := responses.ResponseInputImageDetailAuto
						switch part.OfImageURL.ImageURL.Detail {
						case "low":
							detail = responses.ResponseInputImageDetailLow
						case "high":
							detail = responses.ResponseInputImageDetailHigh
						}
						contentParts = append(contentParts, responses.ResponseInputContentUnionParam{
							OfInputImage: &responses.ResponseInputImageParam{
								ImageURL: openai.String(part.OfImageURL.ImageURL.URL),
								Detail:   detail,
							},
						})
					}
					if part.OfFile != nil {
						fileData := part.OfFile.File.FileData.Value
						fileID := part.OfFile.File.FileID.Value
						filename := part.OfFile.File.Filename.Value
						if fileData == "" && fileID == "" {
							continue
						}
						hasMultimodal = true
						fileParam := &responses.ResponseInputFileParam{}
						if fileData != "" {
							fileParam.FileData = openai.String(fileData)
						}
						if fileID != "" {
							fileParam.FileID = openai.String(fileID)
						}
						if filename != "" {
							fileParam.Filename = openai.String(filename)
						}
						contentParts = append(contentParts, responses.ResponseInputContentUnionParam{
							OfInputFile: fileParam,
						})
					}
					// Note: Audio handled by Chat Completions fallback, skip here
				}
			}

			if textContent != "" {
				textPart := responses.ResponseInputContentUnionParam{
					OfInputText: &responses.ResponseInputTextParam{
						Text: textContent,
					},
				}
				contentParts = append([]responses.ResponseInputContentUnionParam{textPart}, contentParts...)
			}

			if hasMultimodal && len(contentParts) > 0 {
				input = append(input, responses.ResponseInputItemUnionParam{
					OfMessage: &responses.EasyInputMessageParam{
						Role: responses.EasyInputMessageRoleUser,
						Content: responses.EasyInputMessageContentUnionParam{
							OfInputItemContentList: contentParts,
						},
					},
				})
			} else if textContent != "" {
				input = append(input, responses.ResponseInputItemUnionParam{
					OfMessage: &responses.EasyInputMessageParam{
						Role: responses.EasyInputMessageRoleUser,
						Content: responses.EasyInputMessageContentUnionParam{
							OfString: openai.String(textContent),
						},
					},
				})
			}
			continue
		}

		content, role := extractMessageContent(msg)
		if role == "" || content == "" {
			continue
		}

		var responsesRole responses.EasyInputMessageRole
		switch role {
		case "system":
			responsesRole = responses.EasyInputMessageRoleSystem
		case "developer":
			responsesRole = responses.EasyInputMessageRoleDeveloper
		case "assistant":
			responsesRole = responses.EasyInputMessageRoleAssistant
		case "user":
			responsesRole = responses.EasyInputMessageRoleUser
		default:
			continue
		}

		input = append(input, responses.ResponseInputItemUnionParam{
			OfMessage: &responses.EasyInputMessageParam{
				Role: responsesRole,
				Content: responses.EasyInputMessageContentUnionParam{
					OfString: openai.String(content),
				},
			},
		})
	}

	return input
}

// hasAudioContent checks if the prompt contains audio content
func hasAudioContent(messages []openai.ChatCompletionMessageParamUnion) bool {
	for _, msg := range messages {
		if msg.OfUser != nil && len(msg.OfUser.Content.OfArrayOfContentParts) > 0 {
			for _, part := range msg.OfUser.Content.OfArrayOfContentParts {
				if part.OfInputAudio != nil {
					return true
				}
			}
		}
	}
	return false
}

// hasMultimodalContent checks if the prompt contains non-text content (image, file, audio).
func hasMultimodalContent(messages []openai.ChatCompletionMessageParamUnion) bool {
	for _, msg := range messages {
		if msg.OfUser != nil && len(msg.OfUser.Content.OfArrayOfContentParts) > 0 {
			for _, part := range msg.OfUser.Content.OfArrayOfContentParts {
				if part.OfImageURL != nil || part.OfFile != nil || part.OfInputAudio != nil {
					return true
				}
			}
		}
	}
	return false
}
