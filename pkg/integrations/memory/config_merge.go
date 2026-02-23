package memory

import (
	"slices"
	"strings"

	"github.com/beeper/ai-bridge/pkg/agents"
	"github.com/beeper/ai-bridge/pkg/shared/httputil"
)

func MergeSearchConfig(defaults *agents.MemorySearchConfig, overrides *agents.MemorySearchConfig) *ResolvedConfig {
	d := extractFields(defaults)
	o := extractFields(overrides)

	enabled := pickBool(o.enabled, d.enabled, true)
	sessionMemory := pickBool(o.sessionMemory, d.sessionMemory, false)
	provider := pickString(o.provider, d.provider, "auto")
	fallback := pickString(o.fallback, d.fallback, "none")

	hasRemoteConfig := d.hasRemote || o.hasRemote
	includeRemote := hasRemoteConfig || provider == "openai" || provider == "gemini" || provider == "auto"

	remote := RemoteConfig{}
	if includeRemote {
		remote.BaseURL = pickString(o.remoteBaseURL, d.remoteBaseURL, "")
		remote.APIKey = pickString(o.remoteAPIKey, d.remoteAPIKey, "")
		remote.Headers = httputil.MergeHeaders(d.remoteHeaders, o.remoteHeaders)
		remote.Batch = BatchConfig{
			Enabled:        pickBool(o.batchEnabled, d.batchEnabled, true),
			Wait:           pickBool(o.batchWait, d.batchWait, true),
			Concurrency:    max(1, pickInt(o.batchConcurrency, d.batchConcurrency, 2)),
			PollIntervalMs: max(100, pickInt(o.batchPoll, d.batchPoll, 2000)),
			TimeoutMinutes: max(1, pickInt(o.batchTimeout, d.batchTimeout, 60)),
		}
	}

	modelDefault := ""
	switch provider {
	case "gemini":
		modelDefault = DefaultGeminiEmbeddingModel
	case "openai":
		modelDefault = DefaultOpenAIEmbeddingModel
	}
	model := pickString(o.model, d.model, modelDefault)

	rawSources := slices.Concat(d.sources, o.sources)
	sources := normalizeSources(rawSources, sessionMemory)

	rawExtraPaths := slices.Concat(d.extraPaths, o.extraPaths)
	extraPaths := dedupeStrings(rawExtraPaths)

	vector := VectorConfig{
		Enabled:       pickBool(o.vectorEnabled, d.vectorEnabled, true),
		ExtensionPath: pickString(o.vectorExtension, d.vectorExtension, ""),
	}

	store := StoreConfig{
		Driver: "sqlite",
		Path:   "",
		Vector: vector,
	}

	chunkTokens := pickInt(o.chunkTokens, d.chunkTokens, DefaultChunkTokens)
	chunkOverlap := pickInt(o.chunkOverlap, d.chunkOverlap, DefaultChunkOverlap)
	if chunkTokens < 1 {
		chunkTokens = DefaultChunkTokens
	}
	if chunkOverlap < 0 {
		chunkOverlap = 0
	}
	if chunkOverlap >= chunkTokens {
		chunkOverlap = max(0, chunkTokens-1)
	}

	sync := SyncConfig{
		OnSessionStart:  pickBool(o.syncOnStart, d.syncOnStart, true),
		OnSearch:        pickBool(o.syncOnSearch, d.syncOnSearch, true),
		Watch:           pickBool(o.syncWatch, d.syncWatch, true),
		WatchDebounceMs: pickInt(o.syncWatchDebounce, d.syncWatchDebounce, DefaultWatchDebounceMs),
		IntervalMinutes: pickInt(o.syncInterval, d.syncInterval, 0),
		Sessions: SessionSyncConfig{
			DeltaBytes:    pickInt(o.syncDeltaBytes, d.syncDeltaBytes, DefaultSessionDeltaBytes),
			DeltaMessages: pickInt(o.syncDeltaMessages, d.syncDeltaMessages, DefaultSessionDeltaMessages),
			RetentionDays: pickInt(o.syncRetentionDays, d.syncRetentionDays, 0),
		},
	}

	query := QueryConfig{
		MaxResults:       pickInt(o.queryMaxResults, d.queryMaxResults, DefaultMaxResults),
		MinScore:         pickFloat(o.queryMinScore, d.queryMinScore, DefaultMinScore),
		MaxInjectedChars: pickInt(o.queryMaxInjectedChars, d.queryMaxInjectedChars, 0),
		Hybrid: HybridConfig{
			Enabled:             pickBool(o.hybridEnabled, d.hybridEnabled, DefaultHybridEnabled),
			VectorWeight:        pickFloat(o.hybridVectorWeight, d.hybridVectorWeight, DefaultHybridVectorWeight),
			TextWeight:          pickFloat(o.hybridTextWeight, d.hybridTextWeight, DefaultHybridTextWeight),
			CandidateMultiplier: pickInt(o.hybridCandidateMultiplier, d.hybridCandidateMultiplier, DefaultHybridCandidateMultiple),
		},
	}

	cache := CacheConfig{
		Enabled:    pickBool(o.cacheEnabled, d.cacheEnabled, DefaultCacheEnabled),
		MaxEntries: pickInt(o.cacheMaxEntries, d.cacheMaxEntries, 0),
	}

	query.MinScore = min(max(query.MinScore, 0.0), 1.0)
	vectorWeight := min(max(query.Hybrid.VectorWeight, 0.0), 1.0)
	textWeight := min(max(query.Hybrid.TextWeight, 0.0), 1.0)
	sum := vectorWeight + textWeight
	if sum <= 0 {
		query.Hybrid.VectorWeight = DefaultHybridVectorWeight
		query.Hybrid.TextWeight = DefaultHybridTextWeight
	} else {
		query.Hybrid.VectorWeight = vectorWeight / sum
		query.Hybrid.TextWeight = textWeight / sum
	}
	query.Hybrid.CandidateMultiplier = min(max(query.Hybrid.CandidateMultiplier, 1), 20)
	sync.Sessions.DeltaBytes = max(0, sync.Sessions.DeltaBytes)
	sync.Sessions.DeltaMessages = max(0, sync.Sessions.DeltaMessages)
	sync.Sessions.RetentionDays = max(0, sync.Sessions.RetentionDays)

	experimental := ExperimentalConfig{SessionMemory: sessionMemory}

	resolved := &ResolvedConfig{
		Enabled:      enabled,
		Sources:      sources,
		ExtraPaths:   extraPaths,
		Provider:     provider,
		Model:        model,
		Fallback:     fallback,
		Remote:       remote,
		Store:        store,
		Chunking:     ChunkingConfig{Tokens: chunkTokens, Overlap: chunkOverlap},
		Sync:         sync,
		Query:        query,
		Cache:        cache,
		Experimental: experimental,
	}

	if !resolved.Enabled {
		return nil
	}
	return resolved
}

func normalizeSources(input []string, sessionMemoryEnabled bool) []string {
	if len(input) == 0 {
		input = []string{DefaultMemorySource, "workspace"}
	}
	normalized := make(map[string]bool)
	for _, source := range input {
		switch strings.ToLower(strings.TrimSpace(source)) {
		case "memory":
			normalized["memory"] = true
		case "workspace":
			normalized["workspace"] = true
		case "sessions":
			if sessionMemoryEnabled {
				normalized["sessions"] = true
			}
		}
	}
	if len(normalized) == 0 {
		normalized["memory"] = true
	}
	out := make([]string, 0, len(normalized))
	for key := range normalized {
		out = append(out, key)
	}
	return out
}

func pickBool(override, fallback *bool, defaultVal bool) bool {
	if override != nil {
		return *override
	}
	if fallback != nil {
		return *fallback
	}
	return defaultVal
}

func pickString(override, fallback, defaultVal string) string {
	if strings.TrimSpace(override) != "" {
		return override
	}
	if strings.TrimSpace(fallback) != "" {
		return fallback
	}
	return defaultVal
}

func pickInt(override, fallback, defaultVal int) int {
	if override != 0 {
		return override
	}
	if fallback != 0 {
		return fallback
	}
	return defaultVal
}

func pickFloat(override, fallback, defaultVal float64) float64 {
	if override != 0 {
		return override
	}
	if fallback != 0 {
		return fallback
	}
	return defaultVal
}

type searchFields struct {
	enabled                   *bool
	sessionMemory             *bool
	provider                  string
	model                     string
	fallback                  string
	sources                   []string
	extraPaths                []string
	vectorEnabled             *bool
	vectorExtension           string
	chunkTokens               int
	chunkOverlap              int
	syncOnStart               *bool
	syncOnSearch              *bool
	syncWatch                 *bool
	syncWatchDebounce         int
	syncInterval              int
	syncDeltaBytes            int
	syncDeltaMessages         int
	syncRetentionDays         int
	queryMaxResults           int
	queryMinScore             float64
	queryMaxInjectedChars     int
	hybridEnabled             *bool
	hybridVectorWeight        float64
	hybridTextWeight          float64
	hybridCandidateMultiplier int
	cacheEnabled              *bool
	cacheMaxEntries           int
	remoteBaseURL             string
	remoteAPIKey              string
	remoteHeaders             map[string]string
	hasRemote                 bool
	batchEnabled              *bool
	batchWait                 *bool
	batchConcurrency          int
	batchPoll                 int
	batchTimeout              int
}

func extractFields(cfg *agents.MemorySearchConfig) searchFields {
	var f searchFields
	if cfg == nil {
		return f
	}
	f.enabled = cfg.Enabled
	f.provider = cfg.Provider
	f.model = cfg.Model
	f.fallback = cfg.Fallback
	f.sources = cfg.Sources
	f.extraPaths = cfg.ExtraPaths
	if cfg.Experimental != nil {
		f.sessionMemory = cfg.Experimental.SessionMemory
	}
	if cfg.Store != nil && cfg.Store.Vector != nil {
		f.vectorEnabled = cfg.Store.Vector.Enabled
		f.vectorExtension = cfg.Store.Vector.ExtensionPath
	}
	if cfg.Chunking != nil {
		f.chunkTokens = cfg.Chunking.Tokens
		f.chunkOverlap = cfg.Chunking.Overlap
	}
	if cfg.Sync != nil {
		f.syncOnStart = cfg.Sync.OnSessionStart
		f.syncOnSearch = cfg.Sync.OnSearch
		f.syncWatch = cfg.Sync.Watch
		f.syncWatchDebounce = cfg.Sync.WatchDebounceMs
		f.syncInterval = cfg.Sync.IntervalMinutes
		if cfg.Sync.Sessions != nil {
			f.syncDeltaBytes = cfg.Sync.Sessions.DeltaBytes
			f.syncDeltaMessages = cfg.Sync.Sessions.DeltaMessages
			f.syncRetentionDays = cfg.Sync.Sessions.RetentionDays
		}
	}
	if cfg.Query != nil {
		f.queryMaxResults = cfg.Query.MaxResults
		f.queryMinScore = cfg.Query.MinScore
		f.queryMaxInjectedChars = cfg.Query.MaxInjectedChars
		if cfg.Query.Hybrid != nil {
			f.hybridEnabled = cfg.Query.Hybrid.Enabled
			f.hybridVectorWeight = cfg.Query.Hybrid.VectorWeight
			f.hybridTextWeight = cfg.Query.Hybrid.TextWeight
			f.hybridCandidateMultiplier = cfg.Query.Hybrid.CandidateMultiplier
		}
	}
	if cfg.Cache != nil {
		f.cacheEnabled = cfg.Cache.Enabled
		f.cacheMaxEntries = cfg.Cache.MaxEntries
	}
	if cfg.Remote != nil {
		f.remoteBaseURL = cfg.Remote.BaseURL
		f.remoteAPIKey = cfg.Remote.APIKey
		f.remoteHeaders = cfg.Remote.Headers
		f.hasRemote = cfg.Remote.BaseURL != "" || cfg.Remote.APIKey != "" || len(cfg.Remote.Headers) > 0
		if cfg.Remote.Batch != nil {
			f.batchEnabled = cfg.Remote.Batch.Enabled
			f.batchWait = cfg.Remote.Batch.Wait
			f.batchConcurrency = cfg.Remote.Batch.Concurrency
			f.batchPoll = cfg.Remote.Batch.PollIntervalMs
			f.batchTimeout = cfg.Remote.Batch.TimeoutMinutes
		}
	}
	return f
}

func dedupeStrings(input []string) []string {
	if len(input) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(input))
	out := make([]string, 0, len(input))
	for _, raw := range input {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}
