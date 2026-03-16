package ai

import (
	"strings"

	"github.com/beeper/agentremote/sdk"
)

func promptMessagesFromMetadata(meta *MessageMetadata) []PromptMessage {
	if turnData, ok := canonicalTurnData(meta); ok {
		return sdk.PromptMessagesFromTurnData(turnData)
	}
	return nil
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

func promptTail(ctx PromptContext, count int) []PromptMessage {
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

func setCanonicalTurnDataFromPromptMessages(meta *MessageMetadata, messages []PromptMessage) {
	if meta == nil || len(messages) == 0 {
		return
	}
	if turnData, ok := sdk.TurnDataFromUserPromptMessages(messages); ok {
		meta.CanonicalTurnData = turnData.ToMap()
	} else {
		meta.CanonicalTurnData = nil
	}
}
