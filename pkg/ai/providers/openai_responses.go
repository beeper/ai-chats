package providers

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/beeper/ai-bridge/pkg/ai"
	"github.com/beeper/ai-bridge/pkg/ai/utils"
)

type OpenAIResponsesOptions struct {
	StreamOptions    ai.StreamOptions
	ReasoningEffort  ai.ThinkingLevel
	ReasoningSummary string
	ServiceTier      string
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
	messages := ConvertOpenAIResponsesMessages(model, context)
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
		params["tools"] = convertResponsesTools(context.Tools)
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
	messages := make([]map[string]any, 0, len(context.Messages)+1)
	if strings.TrimSpace(context.SystemPrompt) != "" {
		role := "system"
		if model.Reasoning {
			role = "developer"
		}
		messages = append(messages, map[string]any{
			"role":    role,
			"content": utils.SanitizeSurrogates(context.SystemPrompt),
		})
	}

	transformed := TransformMessages(context.Messages, model, nil)
	for _, msg := range transformed {
		switch msg.Role {
		case ai.RoleUser:
			content := []map[string]any{}
			if strings.TrimSpace(msg.Text) != "" {
				content = append(content, map[string]any{
					"type": "input_text",
					"text": utils.SanitizeSurrogates(msg.Text),
				})
			}
			for _, block := range msg.Content {
				if block.Type == ai.ContentTypeText && strings.TrimSpace(block.Text) != "" {
					content = append(content, map[string]any{
						"type": "input_text",
						"text": utils.SanitizeSurrogates(block.Text),
					})
				}
				if block.Type == ai.ContentTypeImage {
					content = append(content, map[string]any{
						"type":      "input_image",
						"detail":    "auto",
						"image_url": "data:" + block.MimeType + ";base64," + block.Data,
					})
				}
			}
			if len(content) == 0 {
				continue
			}
			messages = append(messages, map[string]any{
				"role":    "user",
				"content": content,
			})
		case ai.RoleAssistant:
			for _, block := range msg.Content {
				switch block.Type {
				case ai.ContentTypeText:
					messages = append(messages, map[string]any{
						"type":   "message",
						"role":   "assistant",
						"status": "completed",
						"id":     fallbackTextID(block.TextSignature),
						"content": []map[string]any{{
							"type":        "output_text",
							"text":        utils.SanitizeSurrogates(block.Text),
							"annotations": []any{},
						}},
					})
				case ai.ContentTypeThinking:
					if block.ThinkingSignature != "" {
						// signature payload is already serialized response item.
						// best-effort keep as text fallback when opaque.
						messages = append(messages, map[string]any{
							"type":    "reasoning",
							"summary": []map[string]any{{"type": "summary_text", "text": block.Thinking}},
						})
					}
				case ai.ContentTypeToolCall:
					parts := strings.SplitN(block.ID, "|", 2)
					callID := block.ID
					itemID := ""
					if len(parts) == 2 {
						callID = parts[0]
						itemID = parts[1]
					}
					args := "{}"
					if block.Arguments != nil {
						b, _ := json.Marshal(block.Arguments)
						args = string(b)
					}
					messages = append(messages, map[string]any{
						"type":      "function_call",
						"id":        itemID,
						"call_id":   callID,
						"name":      block.Name,
						"arguments": args,
					})
				}
			}
		case ai.RoleToolResult:
			callID := msg.ToolCallID
			if strings.Contains(callID, "|") {
				callID = strings.SplitN(callID, "|", 2)[0]
			}
			output := "(see attached image)"
			var textParts []string
			for _, block := range msg.Content {
				if block.Type == ai.ContentTypeText {
					textParts = append(textParts, block.Text)
				}
			}
			if len(textParts) > 0 {
				output = strings.Join(textParts, "\n")
			}
			messages = append(messages, map[string]any{
				"type":    "function_call_output",
				"call_id": callID,
				"output":  utils.SanitizeSurrogates(output),
			})
		}
	}
	return messages
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

func convertResponsesTools(tools []ai.Tool) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		out = append(out, map[string]any{
			"type":        "function",
			"name":        tool.Name,
			"description": tool.Description,
			"parameters":  tool.Parameters,
			"strict":      false,
		})
	}
	return out
}
