package connector

import (
	"sync"

	"github.com/openai/openai-go/v3"
	"github.com/pkoukk/tiktoken-go"

	airuntime "github.com/beeper/ai-bridge/pkg/runtime"
)

var (
	tokenizerCache   = make(map[string]*tiktoken.Tiktoken)
	tokenizerCacheMu sync.RWMutex
)

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
		content, role := airuntime.ExtractMessageContent(msg)
		numTokens += len(tkm.Encode(content, nil, nil))
		numTokens += len(tkm.Encode(role, nil, nil))
	}

	numTokens += 3 // Every reply is primed with <|start|>assistant<|message|>

	return numTokens, nil
}

func estimatePromptTokensFallback(messages []openai.ChatCompletionMessageParamUnion) int {
	if len(messages) == 0 {
		return 0
	}

	// Match EstimateTokens structure conservatively:
	// - 3 tokens/message structural overhead
	// - ~1 token/message for role
	// - ceil(chars/4) for message payload
	// - 3 tokens for assistant reply priming
	total := 3
	for _, msg := range messages {
		total += 3
		total += 1
		chars := airuntime.EstimateMessageChars(msg)
		if chars > 0 {
			total += (chars + airuntime.CharsPerTokenEstimate - 1) / airuntime.CharsPerTokenEstimate
		}
	}
	return total
}
