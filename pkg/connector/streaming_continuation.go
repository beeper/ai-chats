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

	// Build function call outputs as input
	var input responses.ResponseInputParam
	if len(state.baseInput) > 0 {
		// All Responses continuations are stateless: include the accumulated local history.
		input = append(input, state.baseInput...)
	}
	for _, approval := range approvalInputs {
		input = append(input, approval)
	}
	for _, output := range pendingOutputs {
		if output.name != "" {
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
			if len(state.baseInput) > 0 {
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

	// Add builtin function tools for this turn.
	// In simple mode this is intentionally restricted to web_search.
	agentID := resolveAgentID(meta)
	strictMode := resolveToolStrictMode(isOpenRouter)
	enabledTools := oc.selectedBuiltinToolsForTurn(ctx, meta)
	if len(enabledTools) > 0 {
		params.Tools = append(params.Tools, ToOpenAITools(enabledTools, strictMode, &oc.log)...)
	}

	// Add boss tools for Boss agent rooms (needed for multi-turn tool use)
	if hasBossAgent(meta) || agents.IsBossAgent(agentID) {
		var enabledBoss []*tools.Tool
		for _, tool := range tools.BossTools() {
			if oc.isToolEnabled(meta, tool.Name) {
				enabledBoss = append(enabledBoss, tool)
			}
		}
		params.Tools = append(params.Tools, bossToolsToOpenAI(enabledBoss, strictMode, &oc.log)...)
	}

	// Add session tools for non-boss agent rooms (needed for multi-turn tool use)
	if oc.getModelCapabilitiesForMeta(meta).SupportsToolCalling && agentID != "" && !(hasBossAgent(meta) || agents.IsBossAgent(agentID)) {
		var enabledSessions []*tools.Tool
		for _, tool := range tools.SessionTools() {
			if oc.isToolEnabled(meta, tool.Name) {
				enabledSessions = append(enabledSessions, tool)
			}
		}
		if len(enabledSessions) > 0 {
			params.Tools = append(params.Tools, bossToolsToOpenAI(enabledSessions, strictMode, &oc.log)...)
		}
	}

	if isOpenRouter {
		params.Tools = renameWebSearchToolParams(params.Tools)
	}

	// Prevent duplicate tool names (Anthropic rejects duplicates)
	logToolParamDuplicates(&oc.log, params.Tools)
	params.Tools = dedupeToolParams(params.Tools)

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
