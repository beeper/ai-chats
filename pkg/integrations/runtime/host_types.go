package runtime

import (
	"maunium.net/go/mautrix/bridgev2/networkid"

	"github.com/openai/openai-go/v3"
)

// MemoryState stores the durable per-portal state owned by the memory integration.
type MemoryState struct {
	CompactionInFlight           bool   `json:"compaction_in_flight,omitempty"`
	LastCompactionAt             int64  `json:"last_compaction_at,omitempty"`
	LastCompactionDroppedCount   int    `json:"last_compaction_dropped_count,omitempty"`
	LastCompactionError          string `json:"last_compaction_error,omitempty"`
	LastCompactionRefreshAt      int64  `json:"last_compaction_refresh_at,omitempty"`
	OverflowFlushAt              int64  `json:"overflow_flush_at,omitempty"`
	OverflowFlushCompactionCount int    `json:"overflow_flush_compaction_count,omitempty"`
	MemoryBootstrapAt            int64  `json:"memory_bootstrap_at,omitempty"`
}

// Meta describes the portal metadata behavior integration modules depend on.
type Meta interface {
	MemoryState() *MemoryState
	EnsureMemoryState() *MemoryState
	AgentID() string
	CompactionCounter() int
	InternalRoom() bool
}

// MessageSummary is a generic message summary.
type MessageSummary struct {
	Role               string
	Body               string
	AgentID            string
	ExcludeFromHistory bool
}

// AssistantMessageInfo is a generic assistant response.
type AssistantMessageInfo struct {
	Body             string
	Model            string
	PromptTokens     int64
	CompletionTokens int64
}

// CompletionToolCall represents a tool call from a model completion.
type CompletionToolCall struct {
	ID       string
	Name     string
	ArgsJSON string
}

// CompletionResult represents a model completion response.
type CompletionResult struct {
	AssistantMessage openai.ChatCompletionMessageParamUnion
	ToolCalls        []CompletionToolCall
	Done             bool
}

// SessionPortalInfo is a generic portal reference for session listing.
type SessionPortalInfo struct {
	Key       string
	PortalKey networkid.PortalKey
}
