package ai

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/beeper/agentremote/sdk"
)

func promptMessagesFromTurnData(td sdk.TurnData) []PromptMessage {
	if td.Role == "" {
		return nil
	}
	switch td.Role {
	case "user":
		msg := PromptMessage{Role: PromptRoleUser}
		for _, part := range td.Parts {
			switch normalizePromptTurnPartType(part.Type) {
			case "text":
				if strings.TrimSpace(part.Text) != "" {
					msg.Blocks = append(msg.Blocks, PromptBlock{Type: PromptBlockText, Text: part.Text})
				}
			case "image":
				imageB64 := promptExtraString(part.Extra, "imageB64")
				if strings.TrimSpace(part.URL) == "" && imageB64 == "" {
					continue
				}
				msg.Blocks = append(msg.Blocks, PromptBlock{
					Type:     PromptBlockImage,
					ImageURL: part.URL,
					ImageB64: imageB64,
					MimeType: part.MediaType,
				})
			}
		}
		if len(msg.Blocks) == 0 {
			return nil
		}
		return []PromptMessage{msg}
	case "assistant":
		assistant := PromptMessage{Role: PromptRoleAssistant}
		var results []PromptMessage
		for _, part := range td.Parts {
			switch normalizePromptTurnPartType(part.Type) {
			case "text":
				if strings.TrimSpace(part.Text) != "" {
					assistant.Blocks = append(assistant.Blocks, PromptBlock{Type: PromptBlockText, Text: part.Text})
				}
			case "reasoning":
				text := strings.TrimSpace(part.Reasoning)
				if text == "" {
					text = strings.TrimSpace(part.Text)
				}
				if text != "" {
					assistant.Blocks = append(assistant.Blocks, PromptBlock{Type: PromptBlockThinking, Text: text})
				}
			case "tool":
				if strings.TrimSpace(part.ToolCallID) != "" && strings.TrimSpace(part.ToolName) != "" {
					assistant.Blocks = append(assistant.Blocks, PromptBlock{
						Type:              PromptBlockToolCall,
						ToolCallID:        part.ToolCallID,
						ToolName:          part.ToolName,
						ToolCallArguments: canonicalPromptToolArguments(part.Input),
					})
				}
				outputText := strings.TrimSpace(formatPromptCanonicalValue(part.Output))
				if outputText == "" {
					outputText = strings.TrimSpace(part.ErrorText)
				}
				if outputText == "" && part.State == "output-denied" {
					outputText = "Denied by user"
				}
				if strings.TrimSpace(part.ToolCallID) != "" && outputText != "" {
					results = append(results, PromptMessage{
						Role:       PromptRoleToolResult,
						ToolCallID: part.ToolCallID,
						ToolName:   part.ToolName,
						IsError:    strings.TrimSpace(part.ErrorText) != "",
						Blocks: []PromptBlock{{
							Type: PromptBlockText,
							Text: outputText,
						}},
					})
				}
			}
		}
		if len(assistant.Blocks) == 0 && len(results) == 0 {
			return nil
		}
		out := make([]PromptMessage, 0, 1+len(results))
		if len(assistant.Blocks) > 0 {
			out = append(out, assistant)
		}
		return append(out, results...)
	default:
		return nil
	}
}

// turnDataFromUserPromptMessages intentionally projects only the latest user
// message because callers pass a single-message tail via promptTail(..., 1).
func turnDataFromUserPromptMessages(messages []PromptMessage) (sdk.TurnData, bool) {
	if len(messages) == 0 {
		return sdk.TurnData{}, false
	}
	msg := messages[0]
	if msg.Role != PromptRoleUser {
		return sdk.TurnData{}, false
	}
	td := sdk.TurnData{Role: "user"}
	td.Parts = make([]sdk.TurnPart, 0, len(msg.Blocks))
	for _, block := range msg.Blocks {
		switch block.Type {
		case PromptBlockText:
			if strings.TrimSpace(block.Text) != "" {
				td.Parts = append(td.Parts, sdk.TurnPart{Type: "text", Text: block.Text})
			}
		case PromptBlockImage:
			if strings.TrimSpace(block.ImageURL) == "" && strings.TrimSpace(block.ImageB64) == "" {
				continue
			}
			part := sdk.TurnPart{Type: "image", URL: block.ImageURL, MediaType: block.MimeType}
			if strings.TrimSpace(block.ImageB64) != "" {
				part.Extra = map[string]any{"imageB64": block.ImageB64}
			}
			td.Parts = append(td.Parts, part)
		}
	}
	return td, len(td.Parts) > 0
}

func promptExtraString(extra map[string]any, key string) string {
	if len(extra) == 0 {
		return ""
	}
	value, _ := extra[key].(string)
	return value
}

func normalizePromptTurnPartType(partType string) string {
	if partType == "dynamic-tool" {
		return "tool"
	}
	return partType
}

func canonicalPromptToolArguments(raw any) string {
	switch typed := raw.(type) {
	case nil:
		return "{}"
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return "{}"
		}
		var decoded any
		if err := json.Unmarshal([]byte(trimmed), &decoded); err == nil {
			data, marshalErr := json.Marshal(decoded)
			if marshalErr == nil && string(data) != "null" {
				return string(data)
			}
		}
		data, err := json.Marshal(typed)
		if err == nil && string(data) != "null" {
			return string(data)
		}
	default:
		if data, err := json.Marshal(typed); err == nil && string(data) != "null" {
			return string(data)
		}
	}
	if value := strings.TrimSpace(formatPromptCanonicalValue(raw)); value != "" {
		data, err := json.Marshal(value)
		if err == nil && string(data) != "null" {
			return string(data)
		}
		return value
	}
	return "{}"
}

func formatPromptCanonicalValue(raw any) string {
	switch typed := raw.(type) {
	case nil:
		return ""
	case string:
		return typed
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprint(typed)
		}
		return string(data)
	}
}
