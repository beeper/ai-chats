package providers

import (
	"os"
	"strings"

	"github.com/beeper/ai-bridge/pkg/ai"
	"github.com/beeper/ai-bridge/pkg/ai/utils"
)

type AnthropicOptions struct {
	StreamOptions        ai.StreamOptions
	ThinkingEnabled      bool
	ThinkingBudgetTokens int
	Effort               string
	InterleavedThinking  bool
	ToolChoice           string
}

type cacheControl struct {
	Type string `json:"type"`
	TTL  string `json:"ttl,omitempty"`
}

func resolveAnthropicCacheRetention(cacheRetention ai.CacheRetention) ai.CacheRetention {
	if cacheRetention != "" {
		return cacheRetention
	}
	if strings.EqualFold(os.Getenv("PI_CACHE_RETENTION"), "long") {
		return ai.CacheRetentionLong
	}
	return ai.CacheRetentionShort
}

func GetAnthropicCacheControl(baseURL string, cacheRetention ai.CacheRetention) (ai.CacheRetention, *cacheControl) {
	retention := resolveAnthropicCacheRetention(cacheRetention)
	if retention == ai.CacheRetentionNone {
		return retention, nil
	}
	cc := &cacheControl{Type: "ephemeral"}
	if retention == ai.CacheRetentionLong && strings.Contains(baseURL, "api.anthropic.com") {
		cc.TTL = "1h"
	}
	return retention, cc
}

func BuildAnthropicParams(model ai.Model, context ai.Context, options AnthropicOptions) map[string]any {
	params := map[string]any{
		"model":      model.ID,
		"stream":     true,
		"max_tokens": max(1024, options.StreamOptions.MaxTokens),
		"messages":   convertAnthropicMessages(model, context),
	}

	_, cache := GetAnthropicCacheControl(model.BaseURL, options.StreamOptions.CacheRetention)
	if strings.TrimSpace(context.SystemPrompt) != "" {
		systemPart := map[string]any{
			"type": "text",
			"text": utils.SanitizeSurrogates(context.SystemPrompt),
		}
		if cache != nil {
			systemPart["cache_control"] = map[string]any{
				"type": cache.Type,
			}
			if cache.TTL != "" {
				systemPart["cache_control"].(map[string]any)["ttl"] = cache.TTL
			}
		}
		params["system"] = []map[string]any{systemPart}
	}

	if options.StreamOptions.Temperature != nil {
		params["temperature"] = *options.StreamOptions.Temperature
	}
	if options.ToolChoice != "" {
		params["tool_choice"] = map[string]any{"type": options.ToolChoice}
	}
	if len(context.Tools) > 0 {
		params["tools"] = convertAnthropicTools(context.Tools)
	}
	if options.ThinkingEnabled {
		thinking := map[string]any{"type": "enabled"}
		if options.ThinkingBudgetTokens > 0 {
			thinking["budget_tokens"] = options.ThinkingBudgetTokens
		}
		if strings.TrimSpace(options.Effort) != "" {
			thinking["effort"] = options.Effort
		}
		params["thinking"] = thinking
	}
	if options.InterleavedThinking {
		params["anthropic-beta"] = "interleaved-thinking-2025-05-14"
	}
	return params
}

func convertAnthropicMessages(model ai.Model, context ai.Context) []map[string]any {
	transformed := TransformMessages(context.Messages, model, func(id string, _ ai.Model, _ ai.Message) string {
		sanitized := strings.Map(func(r rune) rune {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
				return r
			}
			return '_'
		}, id)
		if len(sanitized) > 64 {
			return sanitized[:64]
		}
		return sanitized
	})

	out := make([]map[string]any, 0, len(transformed))
	for _, msg := range transformed {
		switch msg.Role {
		case ai.RoleUser:
			parts := []map[string]any{}
			if strings.TrimSpace(msg.Text) != "" {
				parts = append(parts, map[string]any{
					"type": "text",
					"text": utils.SanitizeSurrogates(msg.Text),
				})
			}
			for _, block := range msg.Content {
				if block.Type == ai.ContentTypeText && strings.TrimSpace(block.Text) != "" {
					parts = append(parts, map[string]any{
						"type": "text",
						"text": utils.SanitizeSurrogates(block.Text),
					})
				}
				if block.Type == ai.ContentTypeImage {
					parts = append(parts, map[string]any{
						"type": "image",
						"source": map[string]any{
							"type":       "base64",
							"media_type": block.MimeType,
							"data":       block.Data,
						},
					})
				}
			}
			if len(parts) == 0 {
				continue
			}
			out = append(out, map[string]any{
				"role":    "user",
				"content": parts,
			})
		case ai.RoleAssistant:
			parts := []map[string]any{}
			for _, block := range msg.Content {
				switch block.Type {
				case ai.ContentTypeText:
					if strings.TrimSpace(block.Text) == "" {
						continue
					}
					parts = append(parts, map[string]any{"type": "text", "text": utils.SanitizeSurrogates(block.Text)})
				case ai.ContentTypeThinking:
					if strings.TrimSpace(block.Thinking) == "" {
						continue
					}
					parts = append(parts, map[string]any{"type": "thinking", "thinking": utils.SanitizeSurrogates(block.Thinking)})
				case ai.ContentTypeToolCall:
					parts = append(parts, map[string]any{
						"type":  "tool_use",
						"id":    block.ID,
						"name":  block.Name,
						"input": block.Arguments,
					})
				}
			}
			if len(parts) == 0 {
				continue
			}
			out = append(out, map[string]any{
				"role":    "assistant",
				"content": parts,
			})
		case ai.RoleToolResult:
			resultText := ""
			for _, block := range msg.Content {
				if block.Type == ai.ContentTypeText {
					if resultText != "" {
						resultText += "\n"
					}
					resultText += block.Text
				}
			}
			if strings.TrimSpace(resultText) == "" {
				resultText = "(see attached image)"
			}
			out = append(out, map[string]any{
				"role": "user",
				"content": []map[string]any{{
					"type":        "tool_result",
					"tool_use_id": msg.ToolCallID,
					"is_error":    msg.IsError,
					"content": []map[string]any{{
						"type": "text",
						"text": utils.SanitizeSurrogates(resultText),
					}},
				}},
			})
		}
	}
	return out
}

func convertAnthropicTools(tools []ai.Tool) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		out = append(out, map[string]any{
			"name":         tool.Name,
			"description":  tool.Description,
			"input_schema": tool.Parameters,
		})
	}
	return out
}
