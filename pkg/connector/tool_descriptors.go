package connector

import (
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared/constant"
	"github.com/rs/zerolog"
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

func sanitizeToolSchema(schema map[string]any, toolName string, log *zerolog.Logger) map[string]any {
	if schema == nil {
		return nil
	}
	sanitized, stripped := sanitizeToolSchemaWithReport(schema)
	logSchemaSanitization(log, toolName, stripped)
	return sanitized
}
