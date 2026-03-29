package ai

import (
	"context"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"

	"github.com/beeper/agentremote/pkg/agents"
	"github.com/beeper/agentremote/pkg/agents/tools"
)

type agentLoopRequestSettings struct {
	model           string
	maxTokens       int
	temperature     *float64
	systemPrompt    string
	reasoningEffort string
}

func (oc *AIClient) buildAgentLoopRequestSettings(meta *PortalMetadata) agentLoopRequestSettings {
	return agentLoopRequestSettings{
		model:           oc.effectiveModelForAPI(meta),
		maxTokens:       oc.effectiveMaxTokens(meta),
		temperature:     oc.effectiveTemperature(meta),
		systemPrompt:    oc.effectivePrompt(meta),
		reasoningEffort: oc.effectiveReasoningEffort(meta),
	}
}

func (oc *AIClient) filterEnabledTools(meta *PortalMetadata, allTools []*tools.Tool) []*tools.Tool {
	var enabled []*tools.Tool
	for _, tool := range allTools {
		if oc.isToolEnabled(meta, tool.Name) {
			enabled = append(enabled, tool)
		}
	}
	return enabled
}

func (oc *AIClient) selectedStreamingToolDescriptors(
	ctx context.Context,
	meta *PortalMetadata,
	allowResolvedBossAgent bool,
) []openAIToolDescriptor {
	if meta != nil && !oc.getModelCapabilitiesForMeta(ctx, meta).SupportsToolCalling {
		return nil
	}

	var descriptors []openAIToolDescriptor
	builtinTools := oc.selectedBuiltinToolsForTurn(ctx, meta)
	if len(builtinTools) > 0 {
		descriptors = append(descriptors, toolDescriptorsFromDefinitions(builtinTools, &oc.log)...)
	}

	if meta == nil {
		return descriptors
	}

	agentID := resolveAgentID(meta)
	isBossRoom := hasBossAgent(meta) || (allowResolvedBossAgent && agents.IsBossAgent(agentID))
	if isBossRoom {
		descriptors = append(descriptors, toolDescriptorsFromBossTools(oc.filterEnabledTools(meta, tools.BossTools()), &oc.log)...)
		return descriptors
	}

	if agentID == "" {
		return descriptors
	}

	descriptors = append(descriptors, toolDescriptorsFromBossTools(oc.filterEnabledTools(meta, tools.SessionTools()), &oc.log)...)
	return descriptors
}

func (oc *AIClient) buildChatCompletionsAgentLoopParams(
	ctx context.Context,
	meta *PortalMetadata,
	messages []openai.ChatCompletionMessageParamUnion,
) openai.ChatCompletionNewParams {
	settings := oc.buildAgentLoopRequestSettings(meta)
	descriptors := oc.selectedStreamingToolDescriptors(ctx, meta, false)
	params := openai.ChatCompletionNewParams{
		Model:    settings.model,
		Messages: messages,
		StreamOptions: openai.ChatCompletionStreamOptionsParam{
			IncludeUsage: param.NewOpt(true),
		},
		Tools: dedupeChatToolParams(descriptorsToChatTools(descriptors, resolveToolStrictMode(oc.isOpenRouterProvider()))),
	}
	if settings.maxTokens > 0 {
		params.MaxCompletionTokens = openai.Int(int64(settings.maxTokens))
	}
	if settings.temperature != nil {
		params.Temperature = openai.Float(*settings.temperature)
	}
	return params
}

func (oc *AIClient) buildResponsesAgentLoopParams(
	ctx context.Context,
	meta *PortalMetadata,
	input responses.ResponseInputParam,
	allowResolvedBossAgent bool,
) responses.ResponseNewParams {
	settings := oc.buildAgentLoopRequestSettings(meta)
	descriptors := oc.selectedStreamingToolDescriptors(ctx, meta, allowResolvedBossAgent)
	params := responses.ResponseNewParams{
		Model: shared.ResponsesModel(settings.model),
		Input: responses.ResponseNewParamsInputUnion{
			OfInputItemList: input,
		},
		Tools: dedupeToolParams(descriptorsToResponsesTools(descriptors, resolveToolStrictMode(oc.isOpenRouterProvider()))),
	}
	if settings.maxTokens > 0 {
		params.MaxOutputTokens = openai.Int(int64(settings.maxTokens))
	}
	if settings.temperature != nil {
		params.Temperature = openai.Float(*settings.temperature)
	}
	if settings.systemPrompt != "" {
		params.Instructions = openai.String(settings.systemPrompt)
	}
	if effort, ok := reasoningEffortMap[settings.reasoningEffort]; ok {
		params.Reasoning = shared.ReasoningParam{
			Effort: shared.ReasoningEffort(effort),
		}
	}
	logToolParamDuplicates(&oc.log, params.Tools)
	return params
}
