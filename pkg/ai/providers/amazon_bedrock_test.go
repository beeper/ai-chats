package providers

import (
	"testing"

	"github.com/beeper/ai-bridge/pkg/ai"
)

func TestBedrockHelperFunctions(t *testing.T) {
	if !SupportsAdaptiveThinking("global.anthropic.claude-opus-4-6-v1") {
		t.Fatalf("expected adaptive thinking support for opus 4.6")
	}
	if SupportsAdaptiveThinking("global.anthropic.claude-sonnet-4-5-v1") {
		t.Fatalf("did not expect adaptive thinking support for sonnet 4.5")
	}

	t.Setenv("PI_CACHE_RETENTION", "long")
	if got := ResolveBedrockCacheRetention(""); got != ai.CacheRetentionLong {
		t.Fatalf("expected env long cache retention, got %s", got)
	}
	if got := ResolveBedrockCacheRetention(ai.CacheRetentionNone); got != ai.CacheRetentionNone {
		t.Fatalf("expected explicit cache retention none to win, got %s", got)
	}

	system := BuildBedrockSystemPrompt(
		"You are helpful",
		ai.Model{ID: "global.anthropic.claude-sonnet-4-5-v1:0"},
		ai.CacheRetentionLong,
	)
	if len(system) != 2 {
		t.Fatalf("expected system + cache point for cacheable model, got %#v", system)
	}
	cachePoint := system[1]["cachePoint"].(map[string]any)
	if cachePoint["ttl"] != "1h" {
		t.Fatalf("expected long cache ttl=1h, got %#v", cachePoint)
	}

	if got := MapBedrockStopReason("TOOL_USE"); got != ai.StopReasonToolUse {
		t.Fatalf("expected TOOL_USE->toolUse, got %s", got)
	}
	if got := MapBedrockStopReason("MAX_TOKENS"); got != ai.StopReasonLength {
		t.Fatalf("expected MAX_TOKENS->length, got %s", got)
	}
	if got := MapBedrockStopReason("OTHER_REASON"); got != ai.StopReasonError {
		t.Fatalf("expected unknown->error, got %s", got)
	}
}

func TestBuildBedrockAdditionalModelRequestFields(t *testing.T) {
	interleaved := true
	fields := BuildBedrockAdditionalModelRequestFields(
		ai.Model{
			ID:        "global.anthropic.claude-sonnet-4-5-v1:0",
			Provider:  "amazon-bedrock",
			API:       ai.APIBedrockConverse,
			Reasoning: true,
		},
		BedrockOptions{
			Reasoning:           ai.ThinkingMedium,
			ThinkingBudgets:     ai.ThinkingBudgets{Medium: 6000},
			InterleavedThinking: &interleaved,
		},
	)
	if fields == nil {
		t.Fatalf("expected additional fields for Claude model reasoning")
	}
	thinking := fields["thinking"].(map[string]any)
	if thinking["type"] != "enabled" || thinking["budget_tokens"] != 6000 {
		t.Fatalf("unexpected non-adaptive thinking payload: %#v", thinking)
	}
	beta := fields["anthropic_beta"].([]string)
	if len(beta) != 1 || beta[0] != "interleaved-thinking-2025-05-14" {
		t.Fatalf("expected interleaved thinking beta flag, got %#v", beta)
	}

	adaptive := BuildBedrockAdditionalModelRequestFields(
		ai.Model{
			ID:        "global.anthropic.claude-opus-4-6-v1",
			Provider:  "amazon-bedrock",
			API:       ai.APIBedrockConverse,
			Reasoning: true,
		},
		BedrockOptions{
			Reasoning: ai.ThinkingXHigh,
		},
	)
	if adaptive["thinking"].(map[string]any)["type"] != "adaptive" {
		t.Fatalf("expected adaptive thinking payload for opus-4-6")
	}
	outputConfig := adaptive["output_config"].(map[string]any)
	if outputConfig["effort"] != "max" {
		t.Fatalf("expected xhigh on opus-4-6 to map to max effort, got %#v", outputConfig["effort"])
	}
}
