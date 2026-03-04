package providers

import (
	"encoding/json"
	"strings"

	"github.com/beeper/ai-bridge/pkg/ai"
	"github.com/beeper/ai-bridge/pkg/ai/utils"
)

type ConvertResponsesMessagesOptions struct {
	IncludeSystemPrompt bool
}

func NormalizeResponsesToolCallID(id string) string {
	if !strings.Contains(id, "|") {
		return id
	}
	parts := strings.SplitN(id, "|", 2)
	callID := sanitizeResponsesIDPart(parts[0], 64)
	itemID := sanitizeResponsesIDPart(parts[1], 64)
	if !strings.HasPrefix(itemID, "fc") {
		itemID = "fc_" + itemID
	}
	callID = strings.TrimRight(callID, "_")
	itemID = strings.TrimRight(itemID, "_")
	if callID == "" {
		callID = "call"
	}
	if itemID == "" {
		itemID = "fc_item"
	}
	return callID + "|" + itemID
}

func ConvertResponsesMessages(
	model ai.Model,
	context ai.Context,
	allowedToolCallProviders map[string]struct{},
	options *ConvertResponsesMessagesOptions,
) []map[string]any {
	includeSystemPrompt := true
	if options != nil {
		includeSystemPrompt = options.IncludeSystemPrompt
	}
	normalizeToolCallID := func(id string, _ ai.Model, _ ai.Message) string {
		if _, ok := allowedToolCallProviders[string(model.Provider)]; !ok {
			return id
		}
		return NormalizeResponsesToolCallID(id)
	}
	transformed := TransformMessages(context.Messages, model, normalizeToolCallID)

	messages := make([]map[string]any, 0, len(transformed)+1)
	if includeSystemPrompt && strings.TrimSpace(context.SystemPrompt) != "" {
		role := "system"
		if model.Reasoning {
			role = "developer"
		}
		messages = append(messages, map[string]any{
			"role":    role,
			"content": utils.SanitizeSurrogates(context.SystemPrompt),
		})
	}

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

func ConvertResponsesTools(tools []ai.Tool, strict bool) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		out = append(out, map[string]any{
			"type":        "function",
			"name":        tool.Name,
			"description": tool.Description,
			"parameters":  tool.Parameters,
			"strict":      strict,
		})
	}
	return out
}

func sanitizeResponsesIDPart(id string, maxLen int) string {
	var b strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	out := b.String()
	if len(out) > maxLen {
		out = out[:maxLen]
	}
	return out
}
