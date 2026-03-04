package providers

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/beeper/ai-bridge/pkg/ai"
	"github.com/beeper/ai-bridge/pkg/ai/utils"
)

type GoogleContent struct {
	Role  string       `json:"role"`
	Parts []GooglePart `json:"parts"`
}

type GooglePart struct {
	Text             string                  `json:"text,omitempty"`
	Thought          bool                    `json:"thought,omitempty"`
	ThoughtSignature string                  `json:"thoughtSignature,omitempty"`
	FunctionCall     *GoogleFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *GoogleFunctionResponse `json:"functionResponse,omitempty"`
	InlineData       *GoogleInlineData       `json:"inlineData,omitempty"`
}

type GoogleFunctionCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args,omitempty"`
	ID   string         `json:"id,omitempty"`
}

type GoogleFunctionResponse struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response,omitempty"`
	ID       string         `json:"id,omitempty"`
	Parts    []GooglePart   `json:"parts,omitempty"`
}

type GoogleInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

func ConvertGoogleTools(tools []ai.Tool, useParameters bool) []map[string]any {
	if len(tools) == 0 {
		return nil
	}
	functions := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		declaration := map[string]any{
			"name":        tool.Name,
			"description": tool.Description,
		}
		if useParameters {
			declaration["parameters"] = tool.Parameters
		} else {
			declaration["parametersJsonSchema"] = tool.Parameters
		}
		functions = append(functions, declaration)
	}
	return []map[string]any{
		{
			"functionDeclarations": functions,
		},
	}
}

func MapGoogleToolChoice(choice string) string {
	switch strings.ToLower(strings.TrimSpace(choice)) {
	case "none":
		return "NONE"
	case "any":
		return "ANY"
	default:
		return "AUTO"
	}
}

func MapGoogleStopReason(reason string) ai.StopReason {
	switch strings.ToUpper(strings.TrimSpace(reason)) {
	case "STOP":
		return ai.StopReasonStop
	case "MAX_TOKENS":
		return ai.StopReasonLength
	case "TOOL_USE":
		return ai.StopReasonToolUse
	default:
		return ai.StopReasonError
	}
}

func IsThinkingPart(part GooglePart) bool {
	return part.Thought
}

func RetainThoughtSignature(existing string, incoming string) string {
	if strings.TrimSpace(incoming) != "" {
		return incoming
	}
	return existing
}

func RequiresToolCallID(modelID string) bool {
	return strings.HasPrefix(modelID, "claude-") || strings.HasPrefix(modelID, "gpt-oss-")
}

func ConvertGoogleMessages(model ai.Model, c ai.Context) []GoogleContent {
	normalized := TransformMessages(c.Messages, model, func(id string, _ ai.Model, _ ai.Message) string {
		if !RequiresToolCallID(model.ID) {
			return id
		}
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

	out := make([]GoogleContent, 0, len(normalized))
	for _, msg := range normalized {
		switch msg.Role {
		case ai.RoleUser:
			parts := make([]GooglePart, 0, max(1, len(msg.Content)))
			if strings.TrimSpace(msg.Text) != "" {
				parts = append(parts, GooglePart{Text: utils.SanitizeSurrogates(msg.Text)})
			}
			for _, block := range msg.Content {
				switch block.Type {
				case ai.ContentTypeText:
					if strings.TrimSpace(block.Text) == "" {
						continue
					}
					parts = append(parts, GooglePart{Text: utils.SanitizeSurrogates(block.Text)})
				case ai.ContentTypeImage:
					if !supportsImageInput(model) {
						continue
					}
					parts = append(parts, GooglePart{
						InlineData: &GoogleInlineData{
							MimeType: block.MimeType,
							Data:     block.Data,
						},
					})
				}
			}
			if len(parts) == 0 {
				continue
			}
			out = append(out, GoogleContent{Role: "user", Parts: parts})
		case ai.RoleAssistant:
			parts := make([]GooglePart, 0, len(msg.Content))
			isSameProviderAndModel := msg.Provider == model.Provider && msg.Model == model.ID
			for _, block := range msg.Content {
				switch block.Type {
				case ai.ContentTypeText:
					if strings.TrimSpace(block.Text) == "" {
						continue
					}
					part := GooglePart{Text: utils.SanitizeSurrogates(block.Text)}
					if isSameProviderAndModel && isValidThoughtSignature(block.TextSignature) {
						part.ThoughtSignature = block.TextSignature
					}
					parts = append(parts, part)
				case ai.ContentTypeThinking:
					if strings.TrimSpace(block.Thinking) == "" {
						continue
					}
					if isSameProviderAndModel {
						part := GooglePart{
							Thought: true,
							Text:    utils.SanitizeSurrogates(block.Thinking),
						}
						if isValidThoughtSignature(block.ThinkingSignature) {
							part.ThoughtSignature = block.ThinkingSignature
						}
						parts = append(parts, part)
					} else {
						parts = append(parts, GooglePart{Text: utils.SanitizeSurrogates(block.Thinking)})
					}
				case ai.ContentTypeToolCall:
					sig := ""
					if isSameProviderAndModel && isValidThoughtSignature(block.ThoughtSignature) {
						sig = block.ThoughtSignature
					}
					isGemini3 := strings.Contains(strings.ToLower(model.ID), "gemini-3")
					if isGemini3 && sig == "" {
						argsBytes, _ := json.MarshalIndent(block.Arguments, "", "  ")
						parts = append(parts, GooglePart{
							Text: fmt.Sprintf(
								`[Historical context: a different model called tool "%s" with arguments: %s. Do not mimic this format - use proper function calling.]`,
								block.Name,
								string(argsBytes),
							),
						})
						continue
					}
					part := GooglePart{
						FunctionCall: &GoogleFunctionCall{
							Name: block.Name,
							Args: block.Arguments,
						},
					}
					if RequiresToolCallID(model.ID) {
						part.FunctionCall.ID = block.ID
					}
					if sig != "" {
						part.ThoughtSignature = sig
					}
					parts = append(parts, part)
				}
			}
			if len(parts) == 0 {
				continue
			}
			out = append(out, GoogleContent{Role: "model", Parts: parts})
		case ai.RoleToolResult:
			textResult := ""
			imageParts := make([]GooglePart, 0)
			for _, block := range msg.Content {
				if block.Type == ai.ContentTypeText {
					if textResult != "" {
						textResult += "\n"
					}
					textResult += block.Text
				}
				if block.Type == ai.ContentTypeImage && supportsImageInput(model) {
					imageParts = append(imageParts, GooglePart{
						InlineData: &GoogleInlineData{
							MimeType: block.MimeType,
							Data:     block.Data,
						},
					})
				}
			}
			respValue := textResult
			if strings.TrimSpace(respValue) == "" && len(imageParts) > 0 {
				respValue = "(see attached image)"
			}
			responseMap := map[string]any{"output": utils.SanitizeSurrogates(respValue)}
			if msg.IsError {
				responseMap = map[string]any{"error": utils.SanitizeSurrogates(respValue)}
			}
			part := GooglePart{
				FunctionResponse: &GoogleFunctionResponse{
					Name:     msg.ToolName,
					Response: responseMap,
				},
			}
			if RequiresToolCallID(model.ID) {
				part.FunctionResponse.ID = msg.ToolCallID
			}
			out = append(out, GoogleContent{
				Role:  "user",
				Parts: []GooglePart{part},
			})
			if len(imageParts) > 0 && !strings.Contains(strings.ToLower(model.ID), "gemini-3") {
				out = append(out, GoogleContent{
					Role:  "user",
					Parts: append([]GooglePart{{Text: "Tool result image:"}}, imageParts...),
				})
			}
		}
	}
	return out
}

func supportsImageInput(model ai.Model) bool {
	for _, input := range model.Input {
		if input == "image" {
			return true
		}
	}
	return false
}

func isValidThoughtSignature(signature string) bool {
	if signature == "" {
		return false
	}
	if len(signature)%4 != 0 {
		return false
	}
	for _, r := range signature {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '+' || r == '/' || r == '=' {
			continue
		}
		return false
	}
	return true
}
