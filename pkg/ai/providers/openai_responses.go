package providers

import (
	"os"
	"strings"

	"github.com/beeper/ai-bridge/pkg/ai"
)

type OpenAIResponsesOptions struct {
	StreamOptions    ai.StreamOptions
	ReasoningEffort  ai.ThinkingLevel
	ReasoningSummary string
	ServiceTier      string
}

var openAIToolCallProviders = map[string]struct{}{
	"openai":       {},
	"openai-codex": {},
	"opencode":     {},
}

func ResolveCacheRetention(cacheRetention ai.CacheRetention) ai.CacheRetention {
	if cacheRetention != "" {
		return cacheRetention
	}
	if strings.EqualFold(os.Getenv("PI_CACHE_RETENTION"), "long") {
		return ai.CacheRetentionLong
	}
	return ai.CacheRetentionShort
}

func GetPromptCacheRetention(baseURL string, cacheRetention ai.CacheRetention) string {
	if cacheRetention != ai.CacheRetentionLong {
		return ""
	}
	if strings.Contains(baseURL, "api.openai.com") {
		return "24h"
	}
	return ""
}

func BuildOpenAIResponsesParams(model ai.Model, context ai.Context, options OpenAIResponsesOptions) map[string]any {
	messages := ConvertResponsesMessages(model, context, openAIToolCallProviders, nil)
	retention := ResolveCacheRetention(options.StreamOptions.CacheRetention)
	params := map[string]any{
		"model":  model.ID,
		"input":  messages,
		"stream": true,
		"store":  false,
	}
	if options.StreamOptions.MaxTokens > 0 {
		params["max_output_tokens"] = options.StreamOptions.MaxTokens
	}
	if options.StreamOptions.Temperature != nil {
		params["temperature"] = *options.StreamOptions.Temperature
	}
	if options.ServiceTier != "" {
		params["service_tier"] = options.ServiceTier
	}
	if context.Tools != nil {
		params["tools"] = ConvertResponsesTools(context.Tools, false)
	}
	if retention != ai.CacheRetentionNone && strings.TrimSpace(options.StreamOptions.SessionID) != "" {
		params["prompt_cache_key"] = options.StreamOptions.SessionID
	}
	if cache := GetPromptCacheRetention(model.BaseURL, retention); cache != "" {
		params["prompt_cache_retention"] = cache
	}
	if model.Reasoning {
		if options.ReasoningEffort != "" || strings.TrimSpace(options.ReasoningSummary) != "" {
			summary := options.ReasoningSummary
			if summary == "" {
				summary = "auto"
			}
			effort := options.ReasoningEffort
			if effort == "" {
				effort = ai.ThinkingMedium
			}
			params["reasoning"] = map[string]any{
				"effort":  string(effort),
				"summary": summary,
			}
			params["include"] = []string{"reasoning.encrypted_content"}
		} else if strings.HasPrefix(strings.ToLower(model.Name), "gpt-5") {
			messages = append(messages, map[string]any{
				"role": "developer",
				"content": []map[string]any{{
					"type": "input_text",
					"text": "# Juice: 0 !important",
				}},
			})
			params["input"] = messages
		}
	}
	return params
}

func ConvertOpenAIResponsesMessages(model ai.Model, context ai.Context) []map[string]any {
	return ConvertResponsesMessages(model, context, openAIToolCallProviders, nil)
}

func fallbackTextID(signature string) string {
	if strings.TrimSpace(signature) != "" {
		if len(signature) > 64 {
			return "msg_" + signature[:16]
		}
		return signature
	}
	return "msg_0"
}
