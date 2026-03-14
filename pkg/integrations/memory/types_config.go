package memory

import (
	memorycore "github.com/beeper/agentremote/pkg/memory"
)

type StoreConfig = memorycore.StoreConfig
type ChunkingConfig = memorycore.ChunkingConfig
type SyncConfig = memorycore.SyncConfig
type SessionSyncConfig = memorycore.SessionSyncConfig
type QueryConfig = memorycore.QueryConfig
type HybridConfig = memorycore.HybridConfig
type CacheConfig = memorycore.CacheConfig
type ExperimentalConfig = memorycore.ExperimentalConfig

const (
	DefaultChunkTokens             = memorycore.DefaultChunkTokens
	DefaultChunkOverlap            = memorycore.DefaultChunkOverlap
	DefaultWatchDebounceMs         = memorycore.DefaultWatchDebounceMs
	DefaultSessionDeltaBytes       = memorycore.DefaultSessionDeltaBytes
	DefaultSessionDeltaMessages    = memorycore.DefaultSessionDeltaMessages
	DefaultMaxResults              = memorycore.DefaultMaxResults
	DefaultMinScore                = memorycore.DefaultMinScore
	DefaultHybridCandidateMultiple = memorycore.DefaultHybridCandidateMultiple
	DefaultCacheEnabled            = memorycore.DefaultCacheEnabled
	UnlimitedCacheEntries          = memorycore.UnlimitedCacheEntries
	DefaultMemorySource            = memorycore.DefaultMemorySource
)
