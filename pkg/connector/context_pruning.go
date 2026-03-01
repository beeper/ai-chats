package connector

import (
	"github.com/openai/openai-go/v3"

	airuntime "github.com/beeper/ai-bridge/pkg/runtime"
)

type PruningConfig = airuntime.PruningConfig

type OverflowFlushConfig = airuntime.OverflowFlushConfig

const (
	charsPerTokenEstimate = airuntime.CharsPerTokenEstimate
	imageCharEstimate     = airuntime.ImageCharEstimate
)

func DefaultPruningConfig() *PruningConfig {
	return airuntime.DefaultPruningConfig()
}

func applyPruningDefaults(config *PruningConfig) *PruningConfig {
	return airuntime.ApplyPruningDefaults(config)
}

func PruneContext(
	prompt []openai.ChatCompletionMessageParamUnion,
	config *PruningConfig,
	contextWindowTokens int,
) []openai.ChatCompletionMessageParamUnion {
	return airuntime.PruneContext(prompt, config, contextWindowTokens)
}

func LimitHistoryTurns(prompt []openai.ChatCompletionMessageParamUnion, limit int) []openai.ChatCompletionMessageParamUnion {
	return airuntime.LimitHistoryTurns(prompt, limit)
}

func smartTruncatePrompt(prompt []openai.ChatCompletionMessageParamUnion, targetReduction float64) []openai.ChatCompletionMessageParamUnion {
	return airuntime.SmartTruncatePrompt(prompt, targetReduction)
}

func estimateMessageChars(msg openai.ChatCompletionMessageParamUnion) int {
	return airuntime.EstimateMessageChars(msg)
}

func makeToolPrunablePredicate(config *PruningConfig) func(toolName string) bool {
	return airuntime.BuildToolPrunablePredicate(config)
}

func softTrimToolResult(content string, config *PruningConfig) string {
	return airuntime.SoftTrimToolResult(content, config)
}
