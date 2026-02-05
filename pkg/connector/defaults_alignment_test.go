package connector

import "testing"

func TestEffectiveTemperatureDefaultUnset(t *testing.T) {
	client := &AIClient{}
	if got := client.effectiveTemperature(nil); got != 0 {
		t.Fatalf("expected default temperature 0 (unset), got %v", got)
	}
}

func TestDefaultThinkLevelModelAware(t *testing.T) {
	client := &AIClient{}

	reasoningMeta := &PortalMetadata{
		Capabilities: ModelCapabilities{
			SupportsReasoning: true,
		},
	}
	if got := client.defaultThinkLevel(reasoningMeta); got != "low" {
		t.Fatalf("expected low for reasoning-capable models, got %q", got)
	}

	nonReasoningMeta := &PortalMetadata{
		Capabilities: ModelCapabilities{
			SupportsReasoning: false,
		},
	}
	if got := client.defaultThinkLevel(nonReasoningMeta); got != "off" {
		t.Fatalf("expected off for non-reasoning models, got %q", got)
	}
}

func TestDefaultThinkLevelHonorsExplicitThinkingLevel(t *testing.T) {
	client := &AIClient{}
	meta := &PortalMetadata{
		ThinkingLevel: "high",
		Capabilities: ModelCapabilities{
			SupportsReasoning: true,
		},
	}

	if got := client.defaultThinkLevel(meta); got != "high" {
		t.Fatalf("expected explicit thinking level to win, got %q", got)
	}
}

func TestDefaultThinkLevelUsesReasoningEffortFallback(t *testing.T) {
	client := &AIClient{}
	meta := &PortalMetadata{
		Capabilities: ModelCapabilities{
			SupportsReasoning: true,
		},
		ReasoningEffort: "medium",
	}

	if got := client.defaultThinkLevel(meta); got != "medium" {
		t.Fatalf("expected medium from reasoning effort, got %q", got)
	}
}
