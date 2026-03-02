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

func TestEstimatePromptTokensFallbackExceedsLegacyForShortPrompts(t *testing.T) {
	prompt := []openai.ChatCompletionMessageParamUnion{
		openai.UserMessage("a"),
		openai.UserMessage("b"),
	}

	legacy := 0
	for _, msg := range prompt {
		legacy += airuntime.EstimateMessageChars(msg) / airuntime.CharsPerTokenEstimate
	}
	if legacy <= 0 {
		legacy = len(prompt) * 3
	}

	got := estimatePromptTokensFallback(prompt)
	if got <= legacy {
		t.Fatalf("expected new fallback estimate to exceed legacy estimate for short prompts, got=%d legacy=%d", got, legacy)
	}
}
