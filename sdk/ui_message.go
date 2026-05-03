package sdk

import (
	"strings"

	"github.com/beeper/ai-chats/pkg/shared/jsonutil"
)

type UIMessageMetadataParams struct {
	TurnID           string
	AgentID          string
	Model            string
	FinishReason     string
	CompletionID     string
	PromptTokens     int64
	CompletionTokens int64
	ReasoningTokens  int64
	TotalTokens      int64
	StartedAtMs      int64
	FirstTokenAtMs   int64
	CompletedAtMs    int64
	IncludeUsage     bool
	Extras           map[string]any
}

func BuildUIMessageMetadata(p UIMessageMetadataParams) map[string]any {
	metadata := map[string]any{}
	if p.TurnID != "" {
		metadata["turn_id"] = p.TurnID
	}
	if p.AgentID != "" {
		metadata["agent_id"] = p.AgentID
	}
	if p.Model != "" {
		metadata["model"] = p.Model
	}
	if p.FinishReason != "" {
		metadata["finish_reason"] = MapFinishReason(p.FinishReason)
	}
	if p.CompletionID != "" {
		metadata["completion_id"] = p.CompletionID
	}
	if p.IncludeUsage && (p.PromptTokens > 0 || p.CompletionTokens > 0 || p.ReasoningTokens > 0) {
		usage := map[string]any{
			"prompt_tokens":     p.PromptTokens,
			"completion_tokens": p.CompletionTokens,
			"reasoning_tokens":  p.ReasoningTokens,
		}
		if p.TotalTokens > 0 {
			usage["total_tokens"] = p.TotalTokens
		}
		metadata["usage"] = usage
	}
	if p.IncludeUsage {
		timing := map[string]any{}
		if p.StartedAtMs > 0 {
			timing["started_at"] = p.StartedAtMs
		}
		if p.FirstTokenAtMs > 0 {
			timing["first_token_at"] = p.FirstTokenAtMs
		}
		if p.CompletedAtMs > 0 {
			timing["completed_at"] = p.CompletedAtMs
		}
		if len(timing) > 0 {
			metadata["timing"] = timing
		}
	}
	if len(p.Extras) > 0 {
		metadata = jsonutil.MergeRecursive(metadata, jsonutil.DeepCloneMap(p.Extras))
	}
	return metadata
}

func MapFinishReason(reason string) string {
	switch strings.TrimSpace(reason) {
	case "stop", "end_turn", "end-turn":
		return "stop"
	case "length", "max_output_tokens":
		return "length"
	case "content_filter", "content-filter":
		return "content-filter"
	case "tool_calls", "tool-calls", "tool_use", "tool-use", "toolUse":
		return "tool-calls"
	case "error":
		return "error"
	default:
		return "other"
	}
}
