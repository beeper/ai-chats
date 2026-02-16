package connector

import (
	"strings"
	"sync"

	"github.com/openai/openai-go/v3"
	"github.com/pkoukk/tiktoken-go"
)

var (
	tokenizerCache   = make(map[string]*tiktoken.Tiktoken)
	tokenizerCacheMu sync.RWMutex
)

// getTokenizer returns a cached tiktoken encoder for the given model
func getTokenizer(model string) (*tiktoken.Tiktoken, error) {
	tokenizerCacheMu.RLock()
	if tkm, ok := tokenizerCache[model]; ok {
		tokenizerCacheMu.RUnlock()
		return tkm, nil
	}
	tokenizerCacheMu.RUnlock()

	tokenizerCacheMu.Lock()
	defer tokenizerCacheMu.Unlock()

	// Double-check after acquiring write lock
	if tkm, ok := tokenizerCache[model]; ok {
		return tkm, nil
	}

	tkm, err := tiktoken.EncodingForModel(model)
	if err != nil {
		// Fall back to cl100k_base for unknown models (GPT-4 family)
		tkm, err = tiktoken.GetEncoding("cl100k_base")
		if err != nil {
			return nil, err
		}
	}

	tokenizerCache[model] = tkm
	return tkm, nil
}

// EstimateTokens counts tokens for a list of chat messages
// Based on OpenAI's cookbook: https://github.com/openai/openai-cookbook
func EstimateTokens(messages []openai.ChatCompletionMessageParamUnion, model string) (int, error) {
	tkm, err := getTokenizer(model)
	if err != nil {
		return 0, err
	}

	// Token overhead per message (consistent across GPT models)
	const tokensPerMessage = 3

	numTokens := 0
	for _, msg := range messages {
		numTokens += tokensPerMessage

		// Extract content and role from the message using the union type fields
		content, role := extractMessageContent(msg)
		numTokens += len(tkm.Encode(content, nil, nil))
		numTokens += len(tkm.Encode(role, nil, nil))
	}

	numTokens += 3 // Every reply is primed with <|start|>assistant<|message|>

	return numTokens, nil
}

// extractMessageContent extracts the text content and role from a message
func extractMessageContent(msg openai.ChatCompletionMessageParamUnion) (content, role string) {
	// Check each possible field in the union
	if msg.OfSystem != nil {
		role = "system"
		content = extractSystemContent(msg.OfSystem.Content)
		return
	}
	if msg.OfUser != nil {
		role = "user"
		content = extractUserContent(msg.OfUser.Content)
		return
	}
	if msg.OfAssistant != nil {
		role = "assistant"
		content = extractAssistantContent(msg.OfAssistant.Content)
		return
	}
	if msg.OfDeveloper != nil {
		role = "developer"
		content = extractDeveloperContent(msg.OfDeveloper.Content)
		return
	}
	if msg.OfTool != nil {
		role = "tool"
		content = extractToolContent(msg.OfTool.Content)
		return
	}
	return "", ""
}

// extractSystemContent extracts text from ChatCompletionSystemMessageParamContentUnion
func extractSystemContent(content openai.ChatCompletionSystemMessageParamContentUnion) string {
	// Try OfString first (most common case)
	if content.OfString.Value != "" {
		return content.OfString.Value
	}
	// Try as array of text parts
	if len(content.OfArrayOfContentParts) > 0 {
		var sb strings.Builder
		for _, part := range content.OfArrayOfContentParts {
			sb.WriteString(part.Text)
		}
		return sb.String()
	}
	return ""
}

// extractUserContent extracts text from ChatCompletionUserMessageParamContentUnion
func extractUserContent(content openai.ChatCompletionUserMessageParamContentUnion) string {
	// Try OfString first
	if content.OfString.Value != "" {
		return content.OfString.Value
	}
	// Try as array of content parts
	if len(content.OfArrayOfContentParts) > 0 {
		var sb strings.Builder
		for _, part := range content.OfArrayOfContentParts {
			if part.OfText != nil {
				sb.WriteString(part.OfText.Text)
			}
		}
		return sb.String()
	}
	return ""
}

// extractAssistantContent extracts text from ChatCompletionAssistantMessageParamContentUnion
func extractAssistantContent(content openai.ChatCompletionAssistantMessageParamContentUnion) string {
	// Try OfString first
	if content.OfString.Value != "" {
		return content.OfString.Value
	}
	// Try as array of content parts
	if len(content.OfArrayOfContentParts) > 0 {
		var sb strings.Builder
		for _, part := range content.OfArrayOfContentParts {
			if part.OfText != nil {
				sb.WriteString(part.OfText.Text)
			}
		}
		return sb.String()
	}
	return ""
}

// extractDeveloperContent extracts text from ChatCompletionDeveloperMessageParamContentUnion
func extractDeveloperContent(content openai.ChatCompletionDeveloperMessageParamContentUnion) string {
	if content.OfString.Value != "" {
		return content.OfString.Value
	}
	if len(content.OfArrayOfContentParts) > 0 {
		var sb strings.Builder
		for _, part := range content.OfArrayOfContentParts {
			sb.WriteString(part.Text)
		}
		return sb.String()
	}
	return ""
}

// extractToolContent extracts text from ChatCompletionToolMessageParamContentUnion
func extractToolContent(content openai.ChatCompletionToolMessageParamContentUnion) string {
	if content.OfString.Value != "" {
		return content.OfString.Value
	}
	if len(content.OfArrayOfContentParts) > 0 {
		var sb strings.Builder
		for _, part := range content.OfArrayOfContentParts {
			sb.WriteString(part.Text)
		}
		return sb.String()
	}
	return ""
}
