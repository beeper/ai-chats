package connector

import (
	"encoding/json"
	"strings"

	aipkg "github.com/beeper/ai-bridge/pkg/ai"
)

// toAIContext maps connector runtime request data into pkg/ai portable context.
// This adapter is intentionally side-effect free to enable incremental migration.
func toAIContext(systemPrompt string, messages []UnifiedMessage, tools []ToolDefinition) aipkg.Context {
	aiMessages := make([]aipkg.Message, 0, len(messages))
	for _, msg := range messages {
		converted := aipkg.Message{
			Role:      mapAIRole(msg.Role),
			Timestamp: 0,
		}

		switch msg.Role {
		case RoleUser:
			blocks := make([]aipkg.ContentBlock, 0, len(msg.Content))
			for _, part := range msg.Content {
				switch part.Type {
				case ContentTypeText:
					converted.Text = part.Text
					blocks = append(blocks, aipkg.ContentBlock{
						Type: aipkg.ContentTypeText,
						Text: part.Text,
					})
				case ContentTypeImage:
					data := part.ImageB64
					if data == "" && strings.HasPrefix(part.ImageURL, "data:") {
						data = strings.TrimPrefix(part.ImageURL, "data:")
					}
					blocks = append(blocks, aipkg.ContentBlock{
						Type:     aipkg.ContentTypeImage,
						Data:     data,
						MimeType: part.MimeType,
					})
				}
			}
			converted.Content = blocks
		case RoleAssistant:
			blocks := make([]aipkg.ContentBlock, 0, len(msg.Content)+len(msg.ToolCalls))
			for _, part := range msg.Content {
				if part.Type == ContentTypeText {
					blocks = append(blocks, aipkg.ContentBlock{
						Type: aipkg.ContentTypeText,
						Text: part.Text,
					})
				}
			}
			for _, tc := range msg.ToolCalls {
				blocks = append(blocks, aipkg.ContentBlock{
					Type:      aipkg.ContentTypeToolCall,
					ID:        tc.ID,
					Name:      tc.Name,
					Arguments: parseToolArguments(tc.Arguments),
				})
			}
			converted.Content = blocks
		case RoleTool:
			converted.ToolCallID = msg.ToolCallID
			converted.ToolName = msg.Name
			text := msg.Text()
			converted.Content = []aipkg.ContentBlock{{
				Type: aipkg.ContentTypeText,
				Text: text,
			}}
			converted.Text = text
		}

		aiMessages = append(aiMessages, converted)
	}

	aiTools := make([]aipkg.Tool, 0, len(tools))
	for _, tool := range tools {
		aiTools = append(aiTools, aipkg.Tool{
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  tool.Parameters,
		})
	}

	return aipkg.Context{
		SystemPrompt: systemPrompt,
		Messages:     aiMessages,
		Tools:        aiTools,
	}
}

func mapAIRole(role MessageRole) aipkg.MessageRole {
	switch role {
	case RoleUser:
		return aipkg.RoleUser
	case RoleAssistant:
		return aipkg.RoleAssistant
	case RoleTool:
		return aipkg.RoleToolResult
	default:
		return aipkg.RoleUser
	}
}

func parseToolArguments(raw string) map[string]any {
	if strings.TrimSpace(raw) == "" {
		return map[string]any{}
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil || parsed == nil {
		return map[string]any{}
	}
	return parsed
}
