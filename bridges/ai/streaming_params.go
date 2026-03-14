package ai

import (
	"context"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote/pkg/agents/tools"
)

// buildResponsesAPIParams creates common Responses API parameters for both streaming and non-streaming paths
func (oc *AIClient) buildResponsesAPIParams(ctx context.Context, portal *bridgev2.Portal, meta *PortalMetadata, messages []openai.ChatCompletionMessageParamUnion) responses.ResponseNewParams {
	log := zerolog.Ctx(ctx)

	params := responses.ResponseNewParams{
		Model:           shared.ResponsesModel(oc.effectiveModelForAPI(meta)),
		MaxOutputTokens: openai.Int(int64(oc.effectiveMaxTokens(meta))),
	}

	systemPrompt := oc.effectivePrompt(meta)
	if systemPrompt != "" {
		params.Instructions = openai.String(systemPrompt)
	}

	// Build full message history for every request.
	input := oc.convertToResponsesInput(messages, meta)
	params.Input = responses.ResponseNewParamsInputUnion{
		OfInputItemList: input,
	}

	// Add reasoning effort when the resolved target supports it.
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

	// Add builtin function tools for this turn.
	// In simple mode this is intentionally restricted to web_search.
	hasAgent := resolveAgentID(meta) != ""
	strictMode := resolveToolStrictMode(isOpenRouter)
	enabledTools := oc.selectedBuiltinToolsForTurn(ctx, meta)
	if len(enabledTools) > 0 {
		params.Tools = append(params.Tools, ToOpenAITools(enabledTools, strictMode, &oc.log)...)
		log.Debug().Int("count", len(enabledTools)).Msg("Added builtin function tools")
	}

	if oc.getModelCapabilitiesForMeta(meta).SupportsToolCalling && hasAgent {
		// Add session tools for non-boss agent rooms.
		if !hasBossAgent(meta) {
			enabledSessions := oc.filterEnabledTools(meta, tools.SessionTools())
			if len(enabledSessions) > 0 {
				params.Tools = append(params.Tools, bossToolsToOpenAI(enabledSessions, strictMode, &oc.log)...)
				log.Debug().Int("count", len(enabledSessions)).Msg("Added session tools")
			}
		}
	}

	// Add boss tools if this is a Boss room
	if hasBossAgent(meta) {
		enabledBoss := oc.filterEnabledTools(meta, tools.BossTools())
		params.Tools = append(params.Tools, bossToolsToOpenAI(enabledBoss, strictMode, &oc.log)...)
		log.Debug().Int("count", len(enabledBoss)).Msg("Added boss agent tools")
	}

	// Prevent duplicate tool names (Anthropic rejects duplicates)
	logToolParamDuplicates(log, params.Tools)
	params.Tools = dedupeToolParams(params.Tools)

	return params
}

// filterEnabledTools returns the subset of tools that are enabled for the current portal.
func (oc *AIClient) filterEnabledTools(meta *PortalMetadata, allTools []*tools.Tool) []*tools.Tool {
	var enabled []*tools.Tool
	for _, tool := range allTools {
		if oc.isToolEnabled(meta, tool.Name) {
			enabled = append(enabled, tool)
		}
	}
	return enabled
}

// bossToolsToOpenAI converts boss tools to OpenAI Responses API format.
func bossToolsToOpenAI(bossTools []*tools.Tool, strictMode ToolStrictMode, log *zerolog.Logger) []responses.ToolUnionParam {
	return descriptorsToResponsesTools(toolDescriptorsFromBossTools(bossTools, log), strictMode)
}

// bossToolsToChatTools converts boss tools to OpenAI Chat Completions tool format.
func bossToolsToChatTools(bossTools []*tools.Tool, log *zerolog.Logger) []openai.ChatCompletionToolUnionParam {
	return descriptorsToChatTools(toolDescriptorsFromBossTools(bossTools, log))
}
