package connector

import (
	"context"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"

	"github.com/beeper/ai-bridge/pkg/agents"
	"github.com/beeper/ai-bridge/pkg/agents/tools"
)

// buildContinuationParams builds params for continuing a response after tool execution
// and/or after responding to tool approval requests.
func (oc *AIClient) buildContinuationParams(
	ctx context.Context,
	state *streamingState,
	meta *PortalMetadata,
	pendingOutputs []functionCallOutput,
	approvalInputs []responses.ResponseInputItemUnionParam,
) responses.ResponseNewParams {
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
	for _, approval := range approvalInputs {
		input = append(input, approval)
	}
	for _, output := range pendingOutputs {
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

	// Add builtin function tools only for agent chats that support tool calling.
	// Model-only chats use a simple prompt without tools to avoid context overflow on small models.
	agentID := resolveAgentID(meta)
	if meta.Capabilities.SupportsToolCalling && agentID != "" {
		enabledTools := oc.enabledBuiltinToolsForModel(ctx, meta)
		if len(enabledTools) > 0 {
			strictMode := resolveToolStrictMode(oc.isOpenRouterProvider())
			params.Tools = append(params.Tools, ToOpenAITools(enabledTools, strictMode, &oc.log)...)
		}
	}

	// Add boss tools for Boss agent rooms (needed for multi-turn tool use)
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

	// Add session tools for non-boss agent rooms (needed for multi-turn tool use)
	if meta.Capabilities.SupportsToolCalling && agentID != "" && !(hasBossAgent(meta) || agents.IsBossAgent(agentID)) {
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
