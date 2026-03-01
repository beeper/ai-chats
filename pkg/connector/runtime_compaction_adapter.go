package connector

import (
	airuntime "github.com/beeper/ai-bridge/pkg/runtime"
	"github.com/openai/openai-go/v3"
)

// CompactionEventType represents a compaction lifecycle event type.
type CompactionEventType string

const (
	CompactionEventStart CompactionEventType = "compaction_start"
	CompactionEventEnd   CompactionEventType = "compaction_end"
)

// CompactionEvent is emitted to clients for overflow compaction visibility.
type CompactionEvent struct {
	Type           CompactionEventType `json:"type"`
	SessionID      string              `json:"session_id,omitempty"`
	MessagesBefore int                 `json:"messages_before,omitempty"`
	MessagesAfter  int                 `json:"messages_after,omitempty"`
	TokensBefore   int                 `json:"tokens_before,omitempty"`
	TokensAfter    int                 `json:"tokens_after,omitempty"`
	Summary        string              `json:"summary,omitempty"`
	WillRetry      bool                `json:"will_retry,omitempty"`
	Error          string              `json:"error,omitempty"`
}

func (oc *AIClient) pruningConfigOrDefault() *airuntime.PruningConfig {
	if oc != nil && oc.connector != nil && oc.connector.Config.Pruning != nil {
		return oc.connector.Config.Pruning
	}
	return airuntime.DefaultPruningConfig()
}

func (oc *AIClient) pruningReserveTokens() int {
	cfg := oc.pruningConfigOrDefault()
	if cfg == nil || cfg.ReserveTokens <= 0 {
		return 2000
	}
	return cfg.ReserveTokens
}

func (oc *AIClient) pruningOverflowFlushConfig() *airuntime.OverflowFlushConfig {
	cfg := oc.pruningConfigOrDefault()
	if cfg == nil {
		return nil
	}
	return cfg.OverflowFlush
}

func estimatePromptTokensForModel(prompt []openai.ChatCompletionMessageParamUnion, model string) int {
	if len(prompt) == 0 {
		return 0
	}
	if count, err := EstimateTokens(prompt, model); err == nil && count > 0 {
		return count
	}
	total := 0
	for _, msg := range prompt {
		total += airuntime.EstimateMessageChars(msg) / airuntime.CharsPerTokenEstimate
	}
	if total <= 0 {
		return len(prompt) * 3
	}
	return total
}
