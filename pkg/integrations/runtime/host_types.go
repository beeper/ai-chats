package runtime

import (
	"strings"
	"time"

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

func (s *MemoryState) ApplyCompactionLifecycle(phase CompactionLifecyclePhase, droppedCount int, errText string, now time.Time) {
	if s == nil {
		return
	}
	switch phase {
	case CompactionLifecycleStart:
		s.CompactionInFlight = true
	case CompactionLifecycleEnd:
		s.CompactionInFlight = false
		s.LastCompactionAt = now.UnixMilli()
		s.LastCompactionDroppedCount = droppedCount
	case CompactionLifecycleFail:
		s.CompactionInFlight = false
		s.LastCompactionError = strings.TrimSpace(errText)
	case CompactionLifecycleRefresh:
		s.LastCompactionRefreshAt = now.UnixMilli()
	}
}

func (s *MemoryState) AlreadyFlushed(compactionCounter int) bool {
	if s == nil || s.OverflowFlushAt == 0 {
		return false
	}
	return s.OverflowFlushCompactionCount == compactionCounter
}

func (s *MemoryState) MarkOverflowFlushed(compactionCounter int, now time.Time) {
	if s == nil {
		return
	}
	s.OverflowFlushAt = now.UnixMilli()
	s.OverflowFlushCompactionCount = compactionCounter
}

func (s *MemoryState) NeedsBootstrap() bool {
	return s == nil || s.MemoryBootstrapAt == 0
}

func (s *MemoryState) MarkBootstrapped(now time.Time) {
	if s == nil {
		return
	}
	s.MemoryBootstrapAt = now.UnixMilli()
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
