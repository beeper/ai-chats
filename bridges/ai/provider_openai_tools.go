package ai

import (
	"fmt"
	"strings"

	"github.com/openai/openai-go/v3/responses"
	"github.com/rs/zerolog"
)

// ToOpenAITools converts tool definitions to OpenAI Responses API format
func ToOpenAITools(tools []ToolDefinition, strictMode ToolStrictMode, log *zerolog.Logger) []responses.ToolUnionParam {
	return descriptorsToResponsesTools(toolDescriptorsFromDefinitions(tools, log), strictMode)
}

// dedupeToolParams removes tools with duplicate identifiers to satisfy providers
// like Anthropic that reject duplicated tool names.
func dedupeToolParams(tools []responses.ToolUnionParam) []responses.ToolUnionParam {
	seen := make(map[string]struct{}, len(tools))
	var result []responses.ToolUnionParam
	for _, t := range tools {
		key := ""
		switch {
		case t.OfFunction != nil:
			key = "function:" + t.OfFunction.Name
		case t.OfWebSearch != nil:
			key = "web_search"
		case t.OfImageGeneration != nil:
			key = "image_generation"
		default:
			key = fmt.Sprintf("%v", t) // fallback, should rarely hit
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, t)
	}
	return result
}

func isOpenRouterBaseURL(baseURL string) bool {
	if baseURL == "" {
		return false
	}
	lowered := strings.ToLower(baseURL)
	return strings.Contains(lowered, "openrouter") || strings.Contains(lowered, "/openrouter/")
}
