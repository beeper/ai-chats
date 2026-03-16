package msgconv

import (
	"strings"

	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/pkg/matrixevents"
	"github.com/beeper/agentremote/pkg/shared/jsonutil"
)

// UIMessageMetadataParams contains parameters for building UI message metadata.
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
}

// BuildUIMessageMetadata builds the metadata map for a com.beeper.ai UIMessage.
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
	return metadata
}

// MergeUIMessageMetadata deep-merges UI message metadata maps so callers can
// safely layer incremental usage/timing updates onto existing state.
func MergeUIMessageMetadata(base, update map[string]any) map[string]any {
	return jsonutil.MergeRecursive(base, update)
}

// UIMessageParams contains parameters for building a full com.beeper.ai UIMessage.
type UIMessageParams struct {
	TurnID     string
	Role       string // "assistant", "user"
	Metadata   map[string]any
	Parts      []map[string]any
	SourceURLs []map[string]any // Optional source-url and source-document parts
	FileParts  []map[string]any // Optional generated file parts
}

// BuildUIMessage builds the complete com.beeper.ai UIMessage payload.
func BuildUIMessage(p UIMessageParams) map[string]any {
	role := p.Role
	if role == "" {
		role = "assistant"
	}
	allParts := p.Parts
	if len(p.SourceURLs) > 0 {
		allParts = append(allParts, p.SourceURLs...)
	}
	if len(p.FileParts) > 0 {
		allParts = append(allParts, p.FileParts...)
	}
	msg := map[string]any{
		"id":    p.TurnID,
		"role":  role,
		"parts": allParts,
	}
	if len(p.Metadata) > 0 {
		msg["metadata"] = p.Metadata
	}
	return msg
}

// RelatesToReplace builds a m.relates_to payload for an edit (m.replace) event.
func RelatesToReplace(initialEventID id.EventID, replyTo id.EventID) map[string]any {
	if initialEventID == "" {
		return nil
	}
	rel := map[string]any{
		"rel_type": matrixevents.RelReplace,
		"event_id": initialEventID.String(),
	}
	if replyTo != "" {
		rel["m.in_reply_to"] = map[string]any{
			"event_id": replyTo.String(),
		}
	}
	return rel
}

// MapFinishReason normalizes provider-specific finish reasons to standard values.
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
