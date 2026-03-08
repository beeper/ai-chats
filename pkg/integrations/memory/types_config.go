package memory

import (
	memorycore "github.com/beeper/ai-bridge/pkg/memory"
)

type RemoteConfig = memorycore.RemoteConfig
type BatchConfig = memorycore.BatchConfig
type StoreConfig = memorycore.StoreConfig
type VectorConfig = memorycore.VectorConfig
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
	DefaultHybridEnabled           = memorycore.DefaultHybridEnabled
	DefaultHybridVectorWeight      = memorycore.DefaultHybridVectorWeight
	DefaultHybridTextWeight        = memorycore.DefaultHybridTextWeight
	DefaultHybridCandidateMultiple = memorycore.DefaultHybridCandidateMultiple
	DefaultCacheEnabled            = memorycore.DefaultCacheEnabled
	DefaultMemorySource            = memorycore.DefaultMemorySource

	DefaultOpenAIEmbeddingModel = memorycore.DefaultOpenAIEmbeddingModel
	DefaultGeminiEmbeddingModel = memorycore.DefaultGeminiEmbeddingModel
)
