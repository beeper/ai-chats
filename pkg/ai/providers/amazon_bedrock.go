package providers

import (
	"os"
	"strings"

	"github.com/beeper/ai-bridge/pkg/ai"
	"github.com/beeper/ai-bridge/pkg/ai/utils"
)

type BedrockToolChoice struct {
	Type string
	Name string
}

type BedrockOptions struct {
	StreamOptions       ai.StreamOptions
	Region              string
	Profile             string
	ToolChoice          any
	Reasoning           ai.ThinkingLevel
	ThinkingBudgets     ai.ThinkingBudgets
	InterleavedThinking *bool
}

func SupportsAdaptiveThinking(modelID string) bool {
	id := strings.ToLower(modelID)
	return strings.Contains(id, "opus-4-6") ||
		strings.Contains(id, "opus-4.6") ||
		strings.Contains(id, "sonnet-4-6") ||
		strings.Contains(id, "sonnet-4.6")
}

func ResolveBedrockCacheRetention(cacheRetention ai.CacheRetention) ai.CacheRetention {
	if cacheRetention != "" {
		return cacheRetention
	}
	if strings.EqualFold(os.Getenv("PI_CACHE_RETENTION"), "long") {
		return ai.CacheRetentionLong
	}
	return ai.CacheRetentionShort
}

func BuildBedrockSystemPrompt(systemPrompt string, model ai.Model, cacheRetention ai.CacheRetention) []map[string]any {
	if strings.TrimSpace(systemPrompt) == "" {
		return nil
	}
	blocks := []map[string]any{
		{"text": utils.SanitizeSurrogates(systemPrompt)},
	}
	if cacheRetention != ai.CacheRetentionNone && supportsBedrockPromptCaching(model) {
		cachePoint := map[string]any{"type": "default"}
		if cacheRetention == ai.CacheRetentionLong {
			cachePoint["ttl"] = "1h"
		}
		blocks = append(blocks, map[string]any{
			"cachePoint": cachePoint,
		})
	}
	return blocks
}

func supportsBedrockPromptCaching(model ai.Model) bool {
	if model.Cost.CacheRead > 0 || model.Cost.CacheWrite > 0 {
		return true
	}
	id := strings.ToLower(model.ID)
	if strings.Contains(id, "claude") && (strings.Contains(id, "-4-") || strings.Contains(id, "-4.")) {
		return true
	}
	if strings.Contains(id, "claude-3-7-sonnet") {
		return true
	}
	if strings.Contains(id, "claude-3-5-haiku") {
		return true
	}
	return false
}

func MapBedrockStopReason(reason string) ai.StopReason {
	switch strings.ToUpper(strings.TrimSpace(reason)) {
	case "END_TURN", "STOP_SEQUENCE":
		return ai.StopReasonStop
	case "MAX_TOKENS", "MODEL_CONTEXT_WINDOW_EXCEEDED":
		return ai.StopReasonLength
	case "TOOL_USE":
		return ai.StopReasonToolUse
	default:
		return ai.StopReasonError
	}
}

func BuildBedrockAdditionalModelRequestFields(model ai.Model, options BedrockOptions) map[string]any {
	if options.Reasoning == "" || !model.Reasoning {
		return nil
	}
	id := strings.ToLower(model.ID)
	if !strings.Contains(id, "anthropic.claude") && !strings.Contains(id, "anthropic/claude") {
		return nil
	}

	if SupportsAdaptiveThinking(model.ID) {
		effort := "high"
		if options.Reasoning == ai.ThinkingXHigh && (strings.Contains(id, "opus-4-6") || strings.Contains(id, "opus-4.6")) {
			effort = "max"
		} else {
			effort = mapBedrockThinkingEffort(options.Reasoning)
		}
		return map[string]any{
			"thinking":      map[string]any{"type": "adaptive"},
			"output_config": map[string]any{"effort": effort},
		}
	}

	level := ClampReasoning(options.Reasoning)
	budgets := mergeThinkingBudgets(ai.ThinkingBudgets{
		Minimal: 1024,
		Low:     2048,
		Medium:  8192,
		High:    16384,
	}, options.ThinkingBudgets)
	budget := budgets.High
	switch level {
	case ai.ThinkingMinimal:
		budget = budgets.Minimal
	case ai.ThinkingLow:
		budget = budgets.Low
	case ai.ThinkingMedium:
		budget = budgets.Medium
	}
	result := map[string]any{
		"thinking": map[string]any{
			"type":          "enabled",
			"budget_tokens": budget,
		},
	}
	interleaved := true
	if options.InterleavedThinking != nil {
		interleaved = *options.InterleavedThinking
	}
	if interleaved {
		result["anthropic_beta"] = []string{"interleaved-thinking-2025-05-14"}
	}
	return result
}

func mapBedrockThinkingEffort(level ai.ThinkingLevel) string {
	switch level {
	case ai.ThinkingMinimal, ai.ThinkingLow:
		return "low"
	case ai.ThinkingMedium:
		return "medium"
	case ai.ThinkingHigh, ai.ThinkingXHigh:
		return "high"
	default:
		return "high"
	}
}
