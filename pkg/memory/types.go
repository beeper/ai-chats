package memory

import "context"

type ResolvedConfig struct {
	Enabled      bool
	Sources      []string
	ExtraPaths   []string
	Provider     string
	Model        string
	Fallback     string
	Remote       RemoteConfig
	Store        StoreConfig
	Chunking     ChunkingConfig
	Sync         SyncConfig
	Query        QueryConfig
	Cache        CacheConfig
	Experimental ExperimentalConfig
}

type RemoteConfig struct {
	BaseURL string
	APIKey  string
	Headers map[string]string
	Batch   BatchConfig
}

type BatchConfig struct {
	Enabled        bool
	Wait           bool
	Concurrency    int
	PollIntervalMs int
	TimeoutMinutes int
}

type StoreConfig struct {
	Driver string
	Path   string
	Vector VectorConfig
}

type VectorConfig struct {
	Enabled       bool
	ExtensionPath string
}

type ChunkingConfig struct {
	Tokens  int
	Overlap int
}

type SyncConfig struct {
	OnSessionStart  bool
	OnSearch        bool
	Watch           bool
	WatchDebounceMs int
	IntervalMinutes int
	Sessions        SessionSyncConfig
}

type SessionSyncConfig struct {
	DeltaBytes    int
	DeltaMessages int
}

type QueryConfig struct {
	MaxResults int
	MinScore   float64
	Hybrid     HybridConfig
}

type HybridConfig struct {
	Enabled             bool
	VectorWeight        float64
	TextWeight          float64
	CandidateMultiplier int
}

type CacheConfig struct {
	Enabled    bool
	MaxEntries int
}

type ExperimentalConfig struct {
	SessionMemory bool
}

type SearchOptions struct {
	MaxResults int
	MinScore   float64
	SessionKey string
}

type SearchResult struct {
	Path      string  `json:"path"`
	StartLine int     `json:"startLine"`
	EndLine   int     `json:"endLine"`
	Score     float64 `json:"score"`
	Snippet   string  `json:"snippet"`
	Source    string  `json:"source"`
}

type EmbeddingProvider interface {
	ID() string
	Model() string
	EmbedQuery(ctx context.Context, text string) ([]float64, error)
	EmbedBatch(ctx context.Context, texts []string) ([][]float64, error)
}

type ProviderStatus struct {
	Provider string
	Model    string
	Fallback *FallbackStatus
}

type FallbackStatus struct {
	From   string `json:"from,omitempty"`
	Reason string `json:"reason,omitempty"`
}
