package sdk

import "strings"

// CanonicalAssistantMetadataParams captures the bridge-specific inputs needed
// to build canonical assistant snapshot and metadata output in one place.
type CanonicalAssistantMetadataParams struct {
	UIMessage          map[string]any
	ToolType           string
	TurnID             string
	AgentID            string
	Role               string
	Body               string
	FinishReason       string
	PromptTokens       int64
	CompletionTokens   int64
	ReasoningTokens    int64
	StartedAtMs        int64
	CompletedAtMs      int64
	Model              string
	CompletionID       string
	FirstTokenAtMs     int64
	ThinkingTokenCount int
}

// CanonicalAssistantMetadata is the combined snapshot/bundle output used by
// bridges that need to persist assistant turns and their canonical metadata.
type CanonicalAssistantMetadata struct {
	Snapshot TurnSnapshot
	Bundle   AssistantMetadataBundle
}

// BuildCanonicalAssistantMetadata assembles the canonical assistant snapshot
// and the shared assistant metadata bundle from one bridge-facing parameter set.
func BuildCanonicalAssistantMetadata(p CanonicalAssistantMetadataParams) CanonicalAssistantMetadata {
	snapshot := BuildTurnSnapshot(p.UIMessage, TurnDataBuildOptions{
		ID:   strings.TrimSpace(p.TurnID),
		Role: strings.TrimSpace(p.Role),
		Text: strings.TrimSpace(p.Body),
		Metadata: map[string]any{
			"turn_id":           strings.TrimSpace(p.TurnID),
			"agent_id":          strings.TrimSpace(p.AgentID),
			"finish_reason":     strings.TrimSpace(p.FinishReason),
			"prompt_tokens":     p.PromptTokens,
			"completion_tokens": p.CompletionTokens,
			"reasoning_tokens":  p.ReasoningTokens,
			"started_at_ms":     p.StartedAtMs,
			"completed_at_ms":   p.CompletedAtMs,
		},
	}, p.ToolType)
	if body := strings.TrimSpace(p.Body); body != "" {
		snapshot.Body = body
	}
	return CanonicalAssistantMetadata{
		Snapshot: snapshot,
		Bundle: BuildAssistantMetadataBundle(AssistantMetadataBundleParams{
			Snapshot:           snapshot,
			FinishReason:       p.FinishReason,
			TurnID:             p.TurnID,
			AgentID:            p.AgentID,
			StartedAtMs:        p.StartedAtMs,
			CompletedAtMs:      p.CompletedAtMs,
			PromptTokens:       p.PromptTokens,
			CompletionTokens:   p.CompletionTokens,
			ReasoningTokens:    p.ReasoningTokens,
			Model:              p.Model,
			CompletionID:       p.CompletionID,
			FirstTokenAtMs:     p.FirstTokenAtMs,
			ThinkingTokenCount: p.ThinkingTokenCount,
		}),
	}
}
