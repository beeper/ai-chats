package providers

import (
	"strings"
	"time"

	"github.com/beeper/ai-bridge/pkg/ai"
)

func TransformMessages(
	messages []ai.Message,
	model ai.Model,
	normalizeToolCallID func(id string, model ai.Model, source ai.Message) string,
) []ai.Message {
	toolCallIDMap := map[string]string{}
	transformed := make([]ai.Message, 0, len(messages))

	for _, msg := range messages {
		switch msg.Role {
		case ai.RoleUser:
			transformed = append(transformed, msg)
		case ai.RoleToolResult:
			if normalized, ok := toolCallIDMap[msg.ToolCallID]; ok && normalized != "" {
				msg.ToolCallID = normalized
			}
			transformed = append(transformed, msg)
		case ai.RoleAssistant:
			isSameModel := msg.Provider == model.Provider && msg.API == model.API && msg.Model == model.ID
			nextContent := make([]ai.ContentBlock, 0, len(msg.Content))
			for _, block := range msg.Content {
				switch block.Type {
				case ai.ContentTypeThinking:
					if block.Redacted {
						if isSameModel {
							nextContent = append(nextContent, block)
						}
						continue
					}
					if strings.TrimSpace(block.Thinking) == "" {
						if isSameModel && block.ThinkingSignature != "" {
							nextContent = append(nextContent, block)
						}
						continue
					}
					if isSameModel {
						nextContent = append(nextContent, block)
					} else {
						nextContent = append(nextContent, ai.ContentBlock{
							Type: ai.ContentTypeText,
							Text: block.Thinking,
						})
					}
				case ai.ContentTypeText:
					nextContent = append(nextContent, block)
				case ai.ContentTypeToolCall:
					if !isSameModel {
						block.ThoughtSignature = ""
						if normalizeToolCallID != nil {
							normalized := normalizeToolCallID(block.ID, model, msg)
							if normalized != "" && normalized != block.ID {
								toolCallIDMap[block.ID] = normalized
								block.ID = normalized
							}
						}
					}
					nextContent = append(nextContent, block)
				default:
					nextContent = append(nextContent, block)
				}
			}
			msg.Content = nextContent
			transformed = append(transformed, msg)
		default:
			transformed = append(transformed, msg)
		}
	}

	// Second pass: synthesize missing tool results for orphaned tool calls.
	result := make([]ai.Message, 0, len(transformed))
	var pendingToolCalls []ai.ContentBlock
	existingToolResultIDs := map[string]struct{}{}
	for _, msg := range transformed {
		switch msg.Role {
		case ai.RoleAssistant:
			if len(pendingToolCalls) > 0 {
				for _, tc := range pendingToolCalls {
					if _, ok := existingToolResultIDs[tc.ID]; ok {
						continue
					}
					result = append(result, ai.Message{
						Role:       ai.RoleToolResult,
						ToolCallID: tc.ID,
						ToolName:   tc.Name,
						Content: []ai.ContentBlock{{
							Type: ai.ContentTypeText,
							Text: "No result provided",
						}},
						IsError:   true,
						Timestamp: time.Now().UnixMilli(),
					})
				}
				pendingToolCalls = nil
				existingToolResultIDs = map[string]struct{}{}
			}

			if msg.StopReason == ai.StopReasonError || msg.StopReason == ai.StopReasonAborted {
				continue
			}

			pendingToolCalls = nil
			existingToolResultIDs = map[string]struct{}{}
			for _, block := range msg.Content {
				if block.Type == ai.ContentTypeToolCall {
					pendingToolCalls = append(pendingToolCalls, block)
				}
			}
			result = append(result, msg)
		case ai.RoleToolResult:
			existingToolResultIDs[msg.ToolCallID] = struct{}{}
			result = append(result, msg)
		case ai.RoleUser:
			if len(pendingToolCalls) > 0 {
				for _, tc := range pendingToolCalls {
					if _, ok := existingToolResultIDs[tc.ID]; ok {
						continue
					}
					result = append(result, ai.Message{
						Role:       ai.RoleToolResult,
						ToolCallID: tc.ID,
						ToolName:   tc.Name,
						Content: []ai.ContentBlock{{
							Type: ai.ContentTypeText,
							Text: "No result provided",
						}},
						IsError:   true,
						Timestamp: time.Now().UnixMilli(),
					})
				}
				pendingToolCalls = nil
				existingToolResultIDs = map[string]struct{}{}
			}
			result = append(result, msg)
		default:
			result = append(result, msg)
		}
	}
	return result
}
