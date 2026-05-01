package memory

import (
	"strings"
	"time"

	iruntime "github.com/beeper/agentremote/pkg/integrations/runtime"
)

// State stores durable per-portal state owned by the memory integration.
type State struct {
	CompactionInFlight           bool   `json:"compaction_in_flight,omitempty"`
	LastCompactionAt             int64  `json:"last_compaction_at,omitempty"`
	LastCompactionDroppedCount   int    `json:"last_compaction_dropped_count,omitempty"`
	LastCompactionError          string `json:"last_compaction_error,omitempty"`
	LastCompactionRefreshAt      int64  `json:"last_compaction_refresh_at,omitempty"`
	OverflowFlushAt              int64  `json:"overflow_flush_at,omitempty"`
	OverflowFlushCompactionCount int    `json:"overflow_flush_compaction_count,omitempty"`
	MemoryBootstrapAt            int64  `json:"memory_bootstrap_at,omitempty"`
}

type Meta interface {
	iruntime.Meta
	MemoryState() *State
	EnsureMemoryState() *State
}

func (s *State) ApplyCompactionLifecycle(phase iruntime.CompactionLifecyclePhase, droppedCount int, errText string, now time.Time) {
	if s == nil {
		return
	}
	switch phase {
	case iruntime.CompactionLifecycleStart:
		s.CompactionInFlight = true
	case iruntime.CompactionLifecycleEnd:
		s.CompactionInFlight = false
		s.LastCompactionAt = now.UnixMilli()
		s.LastCompactionDroppedCount = droppedCount
	case iruntime.CompactionLifecycleFail:
		s.CompactionInFlight = false
		s.LastCompactionError = strings.TrimSpace(errText)
	case iruntime.CompactionLifecycleRefresh:
		s.LastCompactionRefreshAt = now.UnixMilli()
	}
}

func (s *State) AlreadyFlushed(compactionCounter int) bool {
	if s == nil || s.OverflowFlushAt == 0 {
		return false
	}
	return s.OverflowFlushCompactionCount == compactionCounter
}

func (s *State) MarkOverflowFlushed(compactionCounter int, now time.Time) {
	if s == nil {
		return
	}
	s.OverflowFlushAt = now.UnixMilli()
	s.OverflowFlushCompactionCount = compactionCounter
}

func (s *State) NeedsBootstrap() bool {
	return s == nil || s.MemoryBootstrapAt == 0
}

func (s *State) MarkBootstrapped(now time.Time) {
	if s == nil {
		return
	}
	s.MemoryBootstrapAt = now.UnixMilli()
}
