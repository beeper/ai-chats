package memory

const (
	DefaultChunkTokens             = 400
	DefaultChunkOverlap            = 80
	DefaultWatchDebounceMs         = 1500
	DefaultSessionDeltaBytes       = 100_000
	DefaultSessionDeltaMessages    = 50
	DefaultMaxResults              = 6
	DefaultMinScore                = 0.35
	DefaultHybridEnabled           = true
	DefaultHybridVectorWeight      = 0.7
	DefaultHybridTextWeight        = 0.3
	DefaultHybridCandidateMultiple = 4
	DefaultCacheEnabled            = true
	DefaultMemorySource            = "memory"
	DefaultOpenAIEmbeddingModel    = "text-embedding-3-small"
	DefaultGeminiBaseURL           = "https://generativelanguage.googleapis.com/v1beta"
	DefaultGeminiEmbeddingModel    = "gemini-embedding-001"
)
