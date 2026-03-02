package connector

import (
	"strings"

	"github.com/openai/openai-go/v3"

	airuntime "github.com/beeper/ai-bridge/pkg/runtime"
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
		return airuntime.ApplyPruningDefaults(oc.connector.Config.Pruning)
	}
	return airuntime.DefaultPruningConfig()
}

func (oc *AIClient) pruningReserveTokens() int {
	cfg := oc.pruningConfigOrDefault()
	defaults := airuntime.DefaultPruningConfig()
	if cfg == nil {
		return defaults.ReserveTokens
	}
	reserve := cfg.ReserveTokens
	if reserve <= 0 {
		reserve = defaults.ReserveTokens
	}
	if cfg.ReserveTokensFloor > 0 && reserve < cfg.ReserveTokensFloor {
		reserve = cfg.ReserveTokensFloor
	}
	return reserve
}

func (oc *AIClient) pruningMaxHistoryShare() float64 {
	cfg := oc.pruningConfigOrDefault()
	defaults := airuntime.DefaultPruningConfig()
	if cfg == nil || cfg.MaxHistoryShare <= 0 || cfg.MaxHistoryShare >= 1 {
		return defaults.MaxHistoryShare
	}
	return cfg.MaxHistoryShare
}

func (oc *AIClient) pruningCompactionMode() string {
	cfg := oc.pruningConfigOrDefault()
	defaults := airuntime.DefaultPruningConfig()
	if cfg == nil {
		return defaults.CompactionMode
	}
	mode := strings.ToLower(strings.TrimSpace(cfg.CompactionMode))
	switch mode {
	case "default", "safeguard":
		return mode
	default:
		return defaults.CompactionMode
	}
}

func (oc *AIClient) pruningKeepRecentTokens() int {
	cfg := oc.pruningConfigOrDefault()
	defaults := airuntime.DefaultPruningConfig()
	if cfg == nil || cfg.KeepRecentTokens <= 0 {
		return defaults.KeepRecentTokens
	}
	return cfg.KeepRecentTokens
}

func (oc *AIClient) pruningSummarizationEnabled() bool {
	cfg := oc.pruningConfigOrDefault()
	if cfg == nil || cfg.SummarizationEnabled == nil {
		return true
	}
	return *cfg.SummarizationEnabled
}

func (oc *AIClient) pruningMaxSummaryTokens() int {
	cfg := oc.pruningConfigOrDefault()
	defaults := airuntime.DefaultPruningConfig()
	if cfg == nil || cfg.MaxSummaryTokens <= 0 {
		return defaults.MaxSummaryTokens
	}
	return cfg.MaxSummaryTokens
}

func (oc *AIClient) pruningSummarizationModel() string {
	cfg := oc.pruningConfigOrDefault()
	defaults := airuntime.DefaultPruningConfig()
	if cfg == nil || strings.TrimSpace(cfg.SummarizationModel) == "" {
		return defaults.SummarizationModel
	}
	return strings.TrimSpace(cfg.SummarizationModel)
}

func (oc *AIClient) pruningCustomInstructions() string {
	cfg := oc.pruningConfigOrDefault()
	if cfg == nil {
		return ""
	}
	return strings.TrimSpace(cfg.CustomInstructions)
}

func (oc *AIClient) pruningIdentifierPolicy() string {
	cfg := oc.pruningConfigOrDefault()
	if cfg == nil {
		return ""
	}
	return strings.TrimSpace(cfg.IdentifierPolicy)
}

func (oc *AIClient) pruningIdentifierInstructions() string {
	cfg := oc.pruningConfigOrDefault()
	if cfg == nil {
		return ""
	}
	return strings.TrimSpace(cfg.IdentifierInstructions)
}

func (oc *AIClient) pruningPostCompactionRefreshPrompt() string {
	cfg := oc.pruningConfigOrDefault()
	defaults := airuntime.DefaultPruningConfig()
	if cfg == nil || strings.TrimSpace(cfg.PostCompactionRefresh) == "" {
		return defaults.PostCompactionRefresh
	}
	return cfg.PostCompactionRefresh
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
	return estimatePromptTokensFallback(prompt)
}
