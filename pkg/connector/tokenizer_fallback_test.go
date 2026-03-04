package connector

import (
	"testing"

	"github.com/openai/openai-go/v3"

	airuntime "github.com/beeper/ai-bridge/pkg/runtime"
)

func TestEstimatePromptTokensFallbackShortPrompt(t *testing.T) {
	prompt := []openai.ChatCompletionMessageParamUnion{
		openai.UserMessage("a"),
	}

	got := estimatePromptTokensFallback(prompt)
	if got != 8 {
		t.Fatalf("expected fallback estimate 8 for single short prompt, got %d", got)
	}
}

func TestEstimatePromptTokensFallbackExceedsNaiveForShortPrompts(t *testing.T) {
	prompt := []openai.ChatCompletionMessageParamUnion{
		openai.UserMessage("a"),
		openai.UserMessage("b"),
	}

	naive := 0
	for _, msg := range prompt {
		naive += airuntime.EstimateMessageChars(msg) / airuntime.CharsPerTokenEstimate
	}
	if naive <= 0 {
		naive = len(prompt) * 3
	}

	got := estimatePromptTokensFallback(prompt)
	if got <= naive {
		t.Fatalf("expected new fallback estimate to exceed naive estimate for short prompts, got=%d naive=%d", got, naive)
	}
}
