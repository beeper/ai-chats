package ai

import (
	"encoding/json"
	"strings"

	"github.com/beeper/agentremote/sdk"
)

const canonicalPromptSchemaV1 = "ai-bridge-prompt-v1"

func encodePromptMessages(messages []PromptMessage) []map[string]any {
	if len(messages) == 0 {
		return nil
	}
	data, err := json.Marshal(messages)
	if err != nil {
		return nil
	}
	var encoded []map[string]any
	if err = json.Unmarshal(data, &encoded); err != nil {
		return nil
	}
	return encoded
}

func decodePromptMessages(raw []map[string]any) []PromptMessage {
	if len(raw) == 0 {
		return nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var decoded []PromptMessage
	if err = json.Unmarshal(data, &decoded); err != nil {
		return nil
	}
	return decoded
}

func canonicalPromptMessages(meta *MessageMetadata) []PromptMessage {
	if turnData, ok := canonicalTurnData(meta); ok {
		return sdk.PromptMessagesFromTurnData(turnData)
	}
	if meta == nil || meta.CanonicalPromptSchema != canonicalPromptSchemaV1 {
		return nil
	}
	return decodePromptMessages(meta.CanonicalPromptMessages)
}

func filterPromptMessagesForHistory(messages []PromptMessage, injectImages bool) []PromptMessage {
	if len(messages) == 0 {
		return nil
	}
	filtered := make([]PromptMessage, 0, len(messages))
	for _, msg := range messages {
		next := msg
		next.Blocks = filterPromptBlocksForHistory(msg.Blocks, injectImages)
		if len(next.Blocks) == 0 && next.Role != PromptRoleToolResult {
			continue
		}
		if next.Role == PromptRoleToolResult && strings.TrimSpace(next.Text()) == "" {
			continue
		}
		filtered = append(filtered, next)
	}
	return filtered
}

func filterPromptBlocksForHistory(blocks []PromptBlock, injectImages bool) []PromptBlock {
	if len(blocks) == 0 {
		return nil
	}
	filtered := make([]PromptBlock, 0, len(blocks))
	for _, block := range blocks {
		switch block.Type {
		case PromptBlockImage:
			if injectImages {
				filtered = append(filtered, block)
			}
		default:
			filtered = append(filtered, block)
		}
	}
	return filtered
}

func assistantPromptMessagesFromState(state *streamingState) []PromptMessage {
	if state == nil {
		return nil
	}
	assistant := PromptMessage{
		Role:   PromptRoleAssistant,
		Blocks: make([]PromptBlock, 0, 2+len(state.toolCalls)),
	}
	if text := strings.TrimSpace(state.accumulated.String()); text != "" {
		assistant.Blocks = append(assistant.Blocks, PromptBlock{Type: PromptBlockText, Text: text})
	}
	if reasoning := strings.TrimSpace(state.reasoning.String()); reasoning != "" {
		assistant.Blocks = append(assistant.Blocks, PromptBlock{Type: PromptBlockThinking, Text: reasoning})
	}

	resultMessages := make([]PromptMessage, 0, len(state.toolCalls))
	for _, toolCall := range state.toolCalls {
		callID := strings.TrimSpace(toolCall.CallID)
		toolName := strings.TrimSpace(toolCall.ToolName)
		if callID != "" && toolName != "" {
			assistant.Blocks = append(assistant.Blocks, PromptBlock{
				Type:              PromptBlockToolCall,
				ToolCallID:        callID,
				ToolName:          toolName,
				ToolCallArguments: sdk.CanonicalToolArguments(toolCall.Input),
			})
		}

		output := strings.TrimSpace(promptToolOutputText(toolCall))
		if callID == "" || output == "" {
			continue
		}
		resultMessages = append(resultMessages, PromptMessage{
			Role:       PromptRoleToolResult,
			ToolCallID: callID,
			ToolName:   toolName,
			IsError:    toolCall.ErrorMessage != "",
			Blocks: []PromptBlock{{
				Type: PromptBlockText,
				Text: output,
			}},
		})
	}

	if len(assistant.Blocks) == 0 && len(resultMessages) == 0 {
		return nil
	}

	messages := make([]PromptMessage, 0, 1+len(resultMessages))
	if len(assistant.Blocks) > 0 {
		messages = append(messages, assistant)
	}
	messages = append(messages, resultMessages...)
	return messages
}

func promptToolOutputText(toolCall ToolCallMetadata) string {
	switch {
	case len(toolCall.Output) > 0:
		return sdk.FormatCanonicalValue(toolCall.Output)
	case strings.TrimSpace(toolCall.ErrorMessage) != "":
		return strings.TrimSpace(toolCall.ErrorMessage)
	case strings.EqualFold(strings.TrimSpace(toolCall.ResultStatus), "denied"),
		strings.EqualFold(strings.TrimSpace(toolCall.Status), "denied"):
		return "Denied by user"
	default:
		return ""
	}
}

func textPromptMessage(text string) []PromptMessage {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	return []PromptMessage{{
		Role: PromptRoleUser,
		Blocks: []PromptBlock{{
			Type: PromptBlockText,
			Text: text,
		}},
	}}
}

func canonicalPromptTail(ctx PromptContext, count int) []PromptMessage {
	if count <= 0 || len(ctx.Messages) == 0 {
		return nil
	}
	if count > len(ctx.Messages) {
		count = len(ctx.Messages)
	}
	out := make([]PromptMessage, count)
	copy(out, ctx.Messages[len(ctx.Messages)-count:])
	return out
}

func setCanonicalPromptMessages(meta *MessageMetadata, messages []PromptMessage) {
	if meta == nil || len(messages) == 0 {
		return
	}
	if turnData, ok := sdk.TurnDataFromUserPromptMessages(messages); ok {
		meta.CanonicalTurnSchema = sdk.CanonicalTurnDataSchemaV1
		meta.CanonicalTurnData = turnData.ToMap()
	} else {
		meta.CanonicalTurnSchema = ""
		meta.CanonicalTurnData = nil
	}
	meta.CanonicalPromptSchema = canonicalPromptSchemaV1
	meta.CanonicalPromptMessages = encodePromptMessages(messages)
}
