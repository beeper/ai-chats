package ai

import (
	"encoding/json"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared/constant"
	"github.com/rs/zerolog"

	"github.com/beeper/agentremote/pkg/agents/tools"
)

type openAIToolDescriptor struct {
	Name        string
	Description string
	Parameters  map[string]any
}

func toolDescriptorsFromDefinitions(tools []ToolDefinition, log *zerolog.Logger) []openAIToolDescriptor {
	if len(tools) == 0 {
		return nil
	}
	result := make([]openAIToolDescriptor, 0, len(tools))
	for _, tool := range tools {
		result = append(result, openAIToolDescriptor{
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  sanitizeToolSchema(tool.Parameters, tool.Name, log),
		})
	}
	return result
}

func toolDescriptorsFromBossTools(bossTools []*tools.Tool, log *zerolog.Logger) []openAIToolDescriptor {
	if len(bossTools) == 0 {
		return nil
	}
	result := make([]openAIToolDescriptor, 0, len(bossTools))
	for _, tool := range bossTools {
		result = append(result, openAIToolDescriptor{
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  resolveToolSchema(tool.InputSchema, tool.Name, log),
		})
	}
	return result
}

func descriptorsToResponsesTools(descriptors []openAIToolDescriptor, strictMode ToolStrictMode) []responses.ToolUnionParam {
	if len(descriptors) == 0 {
		return nil
	}
	result := make([]responses.ToolUnionParam, 0, len(descriptors))
	for _, tool := range descriptors {
		toolParam := responses.ToolUnionParam{
			OfFunction: &responses.FunctionToolParam{
				Name:       tool.Name,
				Parameters: tool.Parameters,
				Strict:     param.NewOpt(shouldUseStrictMode(strictMode, tool.Parameters)),
				Type:       constant.ValueOf[constant.Function](),
			},
		}
		if tool.Description != "" {
			toolParam.OfFunction.Description = openai.String(tool.Description)
		}
		result = append(result, toolParam)
	}
	return result
}

func descriptorsToChatTools(descriptors []openAIToolDescriptor, strictMode ToolStrictMode) []openai.ChatCompletionToolUnionParam {
	if len(descriptors) == 0 {
		return nil
	}
	result := make([]openai.ChatCompletionToolUnionParam, 0, len(descriptors))
	for _, tool := range descriptors {
		function := openai.FunctionDefinitionParam{
			Name:       tool.Name,
			Parameters: tool.Parameters,
			Strict:     param.NewOpt(shouldUseStrictMode(strictMode, tool.Parameters)),
		}
		if tool.Description != "" {
			function.Description = openai.String(tool.Description)
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

func sanitizeToolSchema(schema map[string]any, toolName string, log *zerolog.Logger) map[string]any {
	if schema == nil {
		return nil
	}
	sanitized, stripped := sanitizeToolSchemaWithReport(schema)
	logSchemaSanitization(log, toolName, stripped)
	return sanitized
}

func resolveToolSchema(inputSchema any, toolName string, log *zerolog.Logger) map[string]any {
	var schema map[string]any
	switch v := inputSchema.(type) {
	case nil:
		return nil
	case map[string]any:
		schema = v
	default:
		encoded, err := json.Marshal(v)
		if err != nil {
			if log != nil {
				log.Error().Err(err).Str("tool_name", toolName).Interface("input_schema", v).Msg("Failed to marshal tool input schema")
			}
			return sanitizeToolSchema(nil, toolName, log)
		}
		if err := json.Unmarshal(encoded, &schema); err != nil {
			if log != nil {
				log.Error().Err(err).Str("tool_name", toolName).Interface("input_schema", v).Msg("Failed to decode tool input schema")
			}
			return sanitizeToolSchema(nil, toolName, log)
		}
	}
	return sanitizeToolSchema(schema, toolName, log)
}
