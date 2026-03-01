package connector

import (
	"context"
	"encoding/json"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
	"github.com/openai/openai-go/v3/shared/constant"
	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/ai-bridge/pkg/agents/tools"
)

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

	// Add builtin function tools only for agent chats that support tool calling.
	// Model-only chats use a simple prompt without tools to avoid context overflow on small models.
	hasAgent := resolveAgentID(meta) != ""
	strictMode := resolveToolStrictMode(isOpenRouter)
	if meta.Capabilities.SupportsToolCalling && hasAgent {
		enabledTools := oc.enabledBuiltinToolsForModel(ctx, meta)
		if len(enabledTools) > 0 {
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
		params.Tools = append(params.Tools, bossToolsToOpenAI(enabledBoss, strictMode, &oc.log)...)
		log.Debug().Int("count", len(enabledBoss)).Msg("Added boss agent tools")
	}

	if isOpenRouter {
		params.Tools = renameWebSearchToolParams(params.Tools)
	}

	// Prevent duplicate tool names (Anthropic rejects duplicates)
	logToolParamDuplicates(log, params.Tools)
	params.Tools = dedupeToolParams(params.Tools)

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
