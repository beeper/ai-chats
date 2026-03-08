package runtime

import (
	"strings"

	"github.com/openai/openai-go/v3"
)

const (
	// CharsPerTokenEstimate approximates token->char conversion for pruning heuristics.
	CharsPerTokenEstimate = 4
	// ImageCharEstimate approximates image token cost for mixed multimodal prompts.
	ImageCharEstimate = 8000
)

// ExtractMessageContent extracts text content and role from a chat message.
func ExtractMessageContent(msg openai.ChatCompletionMessageParamUnion) (content, role string) {
	if msg.OfSystem != nil {
		return ExtractSystemContent(msg.OfSystem.Content), "system"
	}
	if msg.OfUser != nil {
		return ExtractUserContent(msg.OfUser.Content), "user"
	}
	if msg.OfAssistant != nil {
		return ExtractAssistantContent(msg.OfAssistant.Content), "assistant"
	}
	if msg.OfDeveloper != nil {
		return ExtractDeveloperContent(msg.OfDeveloper.Content), "developer"
	}
	if msg.OfTool != nil {
		return ExtractToolContent(msg.OfTool.Content), "tool"
	}
	return "", ""
}

func ExtractSystemContent(content openai.ChatCompletionSystemMessageParamContentUnion) string {
	if content.OfString.Value != "" {
		return content.OfString.Value
	}
	return joinContentText(content.OfArrayOfContentParts, func(part openai.ChatCompletionContentPartTextParam) string {
		return part.Text
	})
}

func ExtractUserContent(content openai.ChatCompletionUserMessageParamContentUnion) string {
	if content.OfString.Value != "" {
		return content.OfString.Value
	}
	return joinOptionalContentText(content.OfArrayOfContentParts, func(part openai.ChatCompletionContentPartUnionParam) *openai.ChatCompletionContentPartTextParam {
		return part.OfText
	})
}

func ExtractAssistantContent(content openai.ChatCompletionAssistantMessageParamContentUnion) string {
	if content.OfString.Value != "" {
		return content.OfString.Value
	}
	return joinOptionalContentText(content.OfArrayOfContentParts, func(part openai.ChatCompletionAssistantMessageParamContentArrayOfContentPartUnion) *openai.ChatCompletionContentPartTextParam {
		return part.OfText
	})
}

func ExtractDeveloperContent(content openai.ChatCompletionDeveloperMessageParamContentUnion) string {
	if content.OfString.Value != "" {
		return content.OfString.Value
	}
	return joinContentText(content.OfArrayOfContentParts, func(part openai.ChatCompletionContentPartTextParam) string {
		return part.Text
	})
}

func ExtractToolContent(content openai.ChatCompletionToolMessageParamContentUnion) string {
	if content.OfString.Value != "" {
		return content.OfString.Value
	}
	return joinContentText(content.OfArrayOfContentParts, func(part openai.ChatCompletionContentPartTextParam) string {
		return part.Text
	})
}

func joinContentText[T any](parts []T, extract func(T) string) string {
	if len(parts) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, part := range parts {
		sb.WriteString(extract(part))
	}
	return sb.String()
}

func joinOptionalContentText[T any](parts []T, extract func(T) *openai.ChatCompletionContentPartTextParam) string {
	return joinContentText(parts, func(part T) string {
		textPart := extract(part)
		if textPart == nil {
			return ""
		}
		return textPart.Text
	})
}

// EstimateMessageChars approximates character usage for one prompt message.
func EstimateMessageChars(msg openai.ChatCompletionMessageParamUnion) int {
	switch {
	case msg.OfSystem != nil:
		return len(ExtractSystemContent(msg.OfSystem.Content))
	case msg.OfUser != nil:
		chars := len(ExtractUserContent(msg.OfUser.Content))
		for _, part := range msg.OfUser.Content.OfArrayOfContentParts {
			if part.OfImageURL != nil {
				chars += ImageCharEstimate
			}
		}
		return chars
	case msg.OfAssistant != nil:
		chars := len(ExtractAssistantContent(msg.OfAssistant.Content))
		for _, tc := range msg.OfAssistant.ToolCalls {
			if tc.OfFunction != nil {
				chars += len(tc.OfFunction.Function.Name) + len(tc.OfFunction.Function.Arguments)
			}
		}
		return chars
	case msg.OfTool != nil:
		return len(ExtractToolContent(msg.OfTool.Content))
	case msg.OfDeveloper != nil:
		return len(ExtractDeveloperContent(msg.OfDeveloper.Content))
	}
	return 0
}

// PromptTextPayloads extracts plain-text payloads from prompt messages for compaction heuristics.
func PromptTextPayloads(prompt []openai.ChatCompletionMessageParamUnion) ([]string, int) {
	texts := make([]string, 0, len(prompt))
	totalChars := 0
	for _, msg := range prompt {
		text, _ := ExtractMessageContent(msg)
		if text == "" {
			continue
		}
		texts = append(texts, text)
		totalChars += len(text)
	}
	return texts, totalChars
}
