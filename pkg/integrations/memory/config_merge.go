package memory

import (
	"slices"
	"strings"

	"github.com/beeper/agentremote/pkg/agents"
	"github.com/beeper/agentremote/pkg/shared/stringutil"
)

func MergeSearchConfig(defaults *agents.MemorySearchConfig, overrides *agents.MemorySearchConfig) *ResolvedConfig {
	d := extractFields(defaults)
	o := extractFields(overrides)

	enabled := pickBool(o.enabled, d.enabled, true)
	sessionMemory := pickBool(o.sessionMemory, d.sessionMemory, false)

	rawSources := slices.Concat(d.sources, o.sources)
	sources := normalizeSources(rawSources, sessionMemory)

	rawExtraPaths := slices.Concat(d.extraPaths, o.extraPaths)
	extraPaths := stringutil.DedupeStrings(rawExtraPaths)

	store := StoreConfig{
		Driver: "sqlite",
		Path:   "",
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
			CandidateMultiplier: pickInt(o.hybridCandidateMultiplier, d.hybridCandidateMultiplier, DefaultHybridCandidateMultiple),
		},
	}

	cache := CacheConfig{
		Enabled:    pickBool(o.cacheEnabled, d.cacheEnabled, DefaultCacheEnabled),
		MaxEntries: normalizeCacheMaxEntries(pickInt(o.cacheMaxEntries, d.cacheMaxEntries, UnlimitedCacheEntries)),
	}

	query.MinScore = min(max(query.MinScore, 0.0), 1.0)
	query.Hybrid.CandidateMultiplier = min(max(query.Hybrid.CandidateMultiplier, 1), 20)
	sync.Sessions.DeltaBytes = max(0, sync.Sessions.DeltaBytes)
	sync.Sessions.DeltaMessages = max(0, sync.Sessions.DeltaMessages)
	sync.Sessions.RetentionDays = max(0, sync.Sessions.RetentionDays)

	experimental := ExperimentalConfig{SessionMemory: sessionMemory}

	resolved := &ResolvedConfig{
		Enabled:      enabled,
		Sources:      sources,
		ExtraPaths:   extraPaths,
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
	seen := make(map[string]struct{})
	var out []string
	for _, source := range input {
		key := strings.ToLower(strings.TrimSpace(source))
		switch key {
		case "memory", "workspace":
		case "sessions":
			if !sessionMemoryEnabled {
				continue
			}
		default:
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	if len(out) == 0 {
		return []string{"memory"}
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

func normalizeCacheMaxEntries(value int) int {
	if value <= 0 {
		return UnlimitedCacheEntries
	}
	return value
}

type searchFields struct {
	enabled                   *bool
	sessionMemory             *bool
	sources                   []string
	extraPaths                []string
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
	hybridCandidateMultiplier int
	cacheEnabled              *bool
	cacheMaxEntries           int
}

func extractFields(cfg *agents.MemorySearchConfig) searchFields {
	var f searchFields
	if cfg == nil {
		return f
	}
	f.enabled = cfg.Enabled
	f.sources = cfg.Sources
	f.extraPaths = cfg.ExtraPaths
	if cfg.Experimental != nil {
		f.sessionMemory = cfg.Experimental.SessionMemory
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
			f.hybridCandidateMultiplier = cfg.Query.Hybrid.CandidateMultiplier
		}
	}
	if cfg.Cache != nil {
		f.cacheEnabled = cfg.Cache.Enabled
		f.cacheMaxEntries = cfg.Cache.MaxEntries
	}
	return f
}
