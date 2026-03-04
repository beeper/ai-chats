package providers

import (
	"encoding/json"
	"strings"

	"github.com/beeper/ai-bridge/pkg/ai"
	"github.com/beeper/ai-bridge/pkg/ai/utils"
)

type OpenAICompletionsOptions struct {
	ToolChoice      any
	ReasoningEffort ai.ThinkingLevel
	StreamOptions   ai.StreamOptions
}

type OpenAIMessage struct {
	Role       string           `json:"role"`
	Content    any              `json:"content,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
	Name       string           `json:"name,omitempty"`
	ToolCalls  []map[string]any `json:"tool_calls,omitempty"`
	Extra      map[string]any   `json:"-"`
}

func BuildOpenAICompletionsParams(model ai.Model, context ai.Context, options OpenAICompletionsOptions) map[string]any {
	compat := GetCompat(model)
	params := map[string]any{
		"model":    model.ID,
		"stream":   true,
		"messages": ConvertOpenAICompletionsMessages(model, context, compat),
	}
	if compat.SupportsUsageInStreaming {
		params["stream_options"] = map[string]any{"include_usage": true}
	}
	if compat.SupportsStore {
		params["store"] = false
	}
	if options.StreamOptions.MaxTokens > 0 {
		if compat.MaxTokensField == "max_tokens" {
			params["max_tokens"] = options.StreamOptions.MaxTokens
		} else {
			params["max_completion_tokens"] = options.StreamOptions.MaxTokens
		}
	}
	if options.StreamOptions.Temperature != nil {
		params["temperature"] = *options.StreamOptions.Temperature
	}
	if len(context.Tools) > 0 {
		params["tools"] = convertOpenAICompletionsTools(context.Tools, compat)
	}
	if options.ToolChoice != nil {
		params["tool_choice"] = options.ToolChoice
	}
	if options.ReasoningEffort != "" && compat.SupportsReasoningEffort {
		params["reasoning_effort"] = mapReasoningEffort(options.ReasoningEffort, compat.ReasoningEffortMap)
	}
	return params
}

func mapReasoningEffort(level ai.ThinkingLevel, custom map[ai.ThinkingLevel]string) string {
	if custom != nil {
		if mapped, ok := custom[level]; ok && mapped != "" {
			return mapped
		}
	}
	return string(level)
}

func convertOpenAICompletionsTools(tools []ai.Tool, compat OpenAICompatResolved) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		fn := map[string]any{
			"name":        tool.Name,
			"description": tool.Description,
			"parameters":  tool.Parameters,
		}
		if compat.SupportsStrictMode {
			fn["strict"] = false
		}
		out = append(out, map[string]any{
			"type":     "function",
			"function": fn,
		})
	}
	return out
}

type OpenAICompatResolved struct {
	SupportsStore                    bool
	SupportsDeveloperRole            bool
	SupportsReasoningEffort          bool
	ReasoningEffortMap               map[ai.ThinkingLevel]string
	SupportsUsageInStreaming         bool
	MaxTokensField                   string
	RequiresToolResultName           bool
	RequiresAssistantAfterToolResult bool
	RequiresThinkingAsText           bool
	RequiresMistralToolIDs           bool
	ThinkingFormat                   string
	SupportsStrictMode               bool
}

func DetectCompat(model ai.Model) OpenAICompatResolved {
	provider := string(model.Provider)
	baseURL := model.BaseURL
	isZai := provider == "zai" || strings.Contains(baseURL, "api.z.ai")
	isMistral := provider == "mistral" || strings.Contains(baseURL, "mistral.ai")
	isGrok := provider == "xai" || strings.Contains(baseURL, "api.x.ai")
	isGroq := provider == "groq" || strings.Contains(baseURL, "groq.com")

	isNonStandard := provider == "cerebras" ||
		strings.Contains(baseURL, "cerebras.ai") ||
		isGrok ||
		isMistral ||
		strings.Contains(baseURL, "chutes.ai") ||
		strings.Contains(baseURL, "deepseek.com") ||
		isZai ||
		provider == "opencode" ||
		strings.Contains(baseURL, "opencode.ai")

	reasoningEffortMap := map[ai.ThinkingLevel]string{}
	if isGroq && model.ID == "qwen/qwen3-32b" {
		reasoningEffortMap[ai.ThinkingMinimal] = "default"
		reasoningEffortMap[ai.ThinkingLow] = "default"
		reasoningEffortMap[ai.ThinkingMedium] = "default"
		reasoningEffortMap[ai.ThinkingHigh] = "default"
		reasoningEffortMap[ai.ThinkingXHigh] = "default"
	}

	return OpenAICompatResolved{
		SupportsStore:                    !isNonStandard,
		SupportsDeveloperRole:            !isNonStandard,
		SupportsReasoningEffort:          !isGrok && !isZai,
		ReasoningEffortMap:               reasoningEffortMap,
		SupportsUsageInStreaming:         true,
		MaxTokensField:                   chooseMaxTokensField(isMistral, baseURL),
		RequiresToolResultName:           isMistral,
		RequiresAssistantAfterToolResult: false,
		RequiresThinkingAsText:           isMistral,
		RequiresMistralToolIDs:           isMistral,
		ThinkingFormat:                   chooseThinkingFormat(isZai),
		SupportsStrictMode:               true,
	}
}

func chooseMaxTokensField(isMistral bool, baseURL string) string {
	if isMistral || strings.Contains(baseURL, "chutes.ai") {
		return "max_tokens"
	}
	return "max_completion_tokens"
}

func chooseThinkingFormat(isZai bool) string {
	if isZai {
		return "zai"
	}
	return "openai"
}

func GetCompat(model ai.Model) OpenAICompatResolved {
	detected := DetectCompat(model)
	if model.Compat == nil {
		return detected
	}
	compat := model.Compat
	if compat.SupportsStore != nil {
		detected.SupportsStore = *compat.SupportsStore
	}
	if compat.SupportsDeveloperRole != nil {
		detected.SupportsDeveloperRole = *compat.SupportsDeveloperRole
	}
	if compat.SupportsReasoningEffort != nil {
		detected.SupportsReasoningEffort = *compat.SupportsReasoningEffort
	}
	if compat.ReasoningEffortMap != nil {
		detected.ReasoningEffortMap = compat.ReasoningEffortMap
	}
	if compat.SupportsUsageInStreaming != nil {
		detected.SupportsUsageInStreaming = *compat.SupportsUsageInStreaming
	}
	if strings.TrimSpace(compat.MaxTokensField) != "" {
		detected.MaxTokensField = compat.MaxTokensField
	}
	if compat.RequiresToolResultName != nil {
		detected.RequiresToolResultName = *compat.RequiresToolResultName
	}
	if compat.RequiresAssistantAfterToolResult != nil {
		detected.RequiresAssistantAfterToolResult = *compat.RequiresAssistantAfterToolResult
	}
	if compat.RequiresThinkingAsText != nil {
		detected.RequiresThinkingAsText = *compat.RequiresThinkingAsText
	}
	if compat.RequiresMistralToolIDs != nil {
		detected.RequiresMistralToolIDs = *compat.RequiresMistralToolIDs
	}
	if strings.TrimSpace(compat.ThinkingFormat) != "" {
		detected.ThinkingFormat = compat.ThinkingFormat
	}
	if compat.SupportsStrictMode != nil {
		detected.SupportsStrictMode = *compat.SupportsStrictMode
	}
	return detected
}

func ConvertOpenAICompletionsMessages(model ai.Model, context ai.Context, compat OpenAICompatResolved) []OpenAIMessage {
	params := make([]OpenAIMessage, 0, len(context.Messages)+1)
	if strings.TrimSpace(context.SystemPrompt) != "" {
		role := "system"
		if model.Reasoning && compat.SupportsDeveloperRole {
			role = "developer"
		}
		params = append(params, OpenAIMessage{
			Role:    role,
			Content: utils.SanitizeSurrogates(context.SystemPrompt),
		})
	}

	normalizeToolCallID := func(id string) string {
		if compat.RequiresMistralToolIDs {
			return normalizeMistralToolID(id)
		}
		if strings.Contains(id, "|") {
			callID := strings.SplitN(id, "|", 2)[0]
			sanitized := sanitizeToolID(callID)
			if len(sanitized) > 40 {
				return sanitized[:40]
			}
			return sanitized
		}
		if model.Provider == "openai" && len(id) > 40 {
			return id[:40]
		}
		return id
	}

	transformed := TransformMessages(context.Messages, model, func(id string, _ ai.Model, _ ai.Message) string {
		return normalizeToolCallID(id)
	})

	lastRole := ""
	for i := 0; i < len(transformed); i++ {
		msg := transformed[i]
		if compat.RequiresAssistantAfterToolResult && lastRole == string(ai.RoleToolResult) && msg.Role == ai.RoleUser {
			params = append(params, OpenAIMessage{Role: "assistant", Content: "I have processed the tool results."})
		}

		switch msg.Role {
		case ai.RoleUser:
			userParts := make([]map[string]any, 0)
			if strings.TrimSpace(msg.Text) != "" {
				userParts = append(userParts, map[string]any{
					"type": "text",
					"text": utils.SanitizeSurrogates(msg.Text),
				})
			}
			for _, part := range msg.Content {
				switch part.Type {
				case ai.ContentTypeText:
					if strings.TrimSpace(part.Text) == "" {
						continue
					}
					userParts = append(userParts, map[string]any{
						"type": "text",
						"text": utils.SanitizeSurrogates(part.Text),
					})
				case ai.ContentTypeImage:
					if !supportsImage(model) {
						continue
					}
					userParts = append(userParts, map[string]any{
						"type": "image_url",
						"image_url": map[string]any{
							"url": "data:" + part.MimeType + ";base64," + part.Data,
						},
					})
				}
			}
			if len(userParts) == 0 {
				continue
			}
			if len(userParts) == 1 && userParts[0]["type"] == "text" {
				params = append(params, OpenAIMessage{
					Role:    "user",
					Content: userParts[0]["text"],
				})
			} else {
				params = append(params, OpenAIMessage{
					Role:    "user",
					Content: userParts,
				})
			}
		case ai.RoleAssistant:
			a := OpenAIMessage{
				Role:    "assistant",
				Content: nil,
			}
			textParts := make([]map[string]any, 0)
			toolCalls := make([]map[string]any, 0)
			for _, part := range msg.Content {
				switch part.Type {
				case ai.ContentTypeText:
					if strings.TrimSpace(part.Text) == "" {
						continue
					}
					textParts = append(textParts, map[string]any{"type": "text", "text": utils.SanitizeSurrogates(part.Text)})
				case ai.ContentTypeThinking:
					if strings.TrimSpace(part.Thinking) == "" {
						continue
					}
					if compat.RequiresThinkingAsText {
						textParts = append([]map[string]any{{
							"type": "text",
							"text": part.Thinking,
						}}, textParts...)
					}
				case ai.ContentTypeToolCall:
					argsBytes, _ := json.Marshal(part.Arguments)
					toolCalls = append(toolCalls, map[string]any{
						"id":   part.ID,
						"type": "function",
						"function": map[string]any{
							"name":      part.Name,
							"arguments": string(argsBytes),
						},
					})
				}
			}
			if len(textParts) > 0 {
				if model.Provider == "github-copilot" {
					text := ""
					for _, p := range textParts {
						text += p["text"].(string)
					}
					a.Content = text
				} else {
					a.Content = textParts
				}
			}
			if len(toolCalls) > 0 {
				a.ToolCalls = toolCalls
			}
			hasContent := false
			switch value := a.Content.(type) {
			case string:
				hasContent = strings.TrimSpace(value) != ""
			case []map[string]any:
				hasContent = len(value) > 0
			}
			if !hasContent && len(a.ToolCalls) == 0 {
				continue
			}
			params = append(params, a)
		case ai.RoleToolResult:
			imageBlocks := make([]map[string]any, 0)
			j := i
			for ; j < len(transformed) && transformed[j].Role == ai.RoleToolResult; j++ {
				toolMsg := transformed[j]
				textResult := ""
				for _, block := range toolMsg.Content {
					if block.Type == ai.ContentTypeText {
						if textResult != "" {
							textResult += "\n"
						}
						textResult += block.Text
					}
				}
				hasText := strings.TrimSpace(textResult) != ""
				msgPayload := OpenAIMessage{
					Role:       "tool",
					ToolCallID: toolMsg.ToolCallID,
					Content:    utils.SanitizeSurrogates(textResult),
				}
				if !hasText {
					msgPayload.Content = "(see attached image)"
				}
				if compat.RequiresToolResultName && toolMsg.ToolName != "" {
					msgPayload.Name = toolMsg.ToolName
				}
				params = append(params, msgPayload)

				if supportsImage(model) {
					for _, block := range toolMsg.Content {
						if block.Type == ai.ContentTypeImage {
							imageBlocks = append(imageBlocks, map[string]any{
								"type": "image_url",
								"image_url": map[string]any{
									"url": "data:" + block.MimeType + ";base64," + block.Data,
								},
							})
						}
					}
				}
			}
			i = j - 1
			if len(imageBlocks) > 0 {
				if compat.RequiresAssistantAfterToolResult {
					params = append(params, OpenAIMessage{Role: "assistant", Content: "I have processed the tool results."})
				}
				content := []map[string]any{{"type": "text", "text": "Attached image(s) from tool result:"}}
				content = append(content, imageBlocks...)
				params = append(params, OpenAIMessage{Role: "user", Content: content})
				lastRole = "user"
			} else {
				lastRole = string(ai.RoleToolResult)
			}
			continue
		}
		lastRole = string(msg.Role)
	}
	return params
}

func supportsImage(model ai.Model) bool {
	for _, in := range model.Input {
		if in == "image" {
			return true
		}
	}
	return false
}

func sanitizeToolID(id string) string {
	var b strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String()
}

func normalizeMistralToolID(id string) string {
	normalized := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return r
		}
		return -1
	}, id)
	if len(normalized) < 9 {
		padding := "ABCDEFGHI"
		return normalized + padding[:9-len(normalized)]
	}
	if len(normalized) > 9 {
		return normalized[:9]
	}
	return normalized
}
