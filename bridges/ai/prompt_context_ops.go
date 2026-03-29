package ai

import "strings"

func ClonePromptMessages(messages []PromptMessage) []PromptMessage {
	if len(messages) == 0 {
		return nil
	}
	out := make([]PromptMessage, 0, len(messages))
	for _, message := range messages {
		cloned := message
		if len(message.Blocks) > 0 {
			cloned.Blocks = append([]PromptBlock(nil), message.Blocks...)
		}
		out = append(out, cloned)
	}
	return out
}

func ClonePromptContext(ctx PromptContext) PromptContext {
	cloned := ctx
	cloned.Messages = ClonePromptMessages(ctx.Messages)
	if len(ctx.Tools) > 0 {
		cloned.Tools = append([]ToolDefinition(nil), ctx.Tools...)
	}
	return cloned
}


func PromptContextMessageCount(ctx PromptContext) int {
	count := len(ctx.Messages)
	if strings.TrimSpace(ctx.SystemPrompt) != "" {
		count++
	}
	return count
}

func newUserTextPromptMessage(text string) PromptMessage {
	return PromptMessage{
		Role: PromptRoleUser,
		Blocks: []PromptBlock{{
			Type: PromptBlockText,
			Text: text,
		}},
	}
}
