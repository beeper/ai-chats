package connector

import (
	"context"
	"fmt"
	"strings"

	"github.com/beeper/ai-bridge/pkg/agents"
	"github.com/beeper/ai-bridge/pkg/memory"
	"github.com/beeper/ai-bridge/pkg/memory/embedding"
)

func resolveMemorySearchConfig(client *AIClient, agentID string) (*memory.ResolvedConfig, error) {
	if client == nil || client.connector == nil {
		return nil, fmt.Errorf("missing connector")
	}
	defaults := client.connector.Config.MemorySearch
	var overrides *agents.MemorySearchConfig

	if agentID != "" {
		store := NewAgentStoreAdapter(client)
		agent, err := store.GetAgentByID(client.backgroundContext(context.TODO()), agentID)
		if err == nil && agent != nil {
			overrides = agent.MemorySearch
		}
	}

	resolved := mergeMemorySearchConfig(defaults, overrides)
	if resolved == nil {
		return nil, fmt.Errorf("memory search disabled")
	}
	return resolved, nil
}

func mergeMemorySearchConfig(
	defaults *MemorySearchConfig,
	overrides *agents.MemorySearchConfig,
) *memory.ResolvedConfig {
	enabled := pickBool(overridesEnabled(overrides), defaultsEnabled(defaults), true)
	sessionMemory := pickBool(overridesSessionMemory(overrides), defaultsSessionMemory(defaults), false)
	provider := pickString(overridesProvider(overrides), defaultsProvider(defaults), "auto")
	fallback := pickString(overridesFallback(overrides), defaultsFallback(defaults), "none")

	hasRemoteConfig := hasRemoteDefaults(defaults) || hasRemoteOverrides(overrides)
	includeRemote := hasRemoteConfig || provider == "openai" || provider == "gemini" || provider == "auto"

	remote := memory.RemoteConfig{}
	if includeRemote {
		remote.BaseURL = pickString(overridesRemoteBaseURL(overrides), defaultsRemoteBaseURL(defaults), "")
		remote.APIKey = pickString(overridesRemoteAPIKey(overrides), defaultsRemoteAPIKey(defaults), "")
		remote.Headers = mergeHeaders(defaultsRemoteHeaders(defaults), overridesRemoteHeaders(overrides))
		remote.Batch = memory.BatchConfig{
			Enabled:        pickBool(overridesBatchEnabled(overrides), defaultsBatchEnabled(defaults), true),
			Wait:           pickBool(overridesBatchWait(overrides), defaultsBatchWait(defaults), true),
			Concurrency:    maxInt(1, pickInt(overridesBatchConcurrency(overrides), defaultsBatchConcurrency(defaults), 2)),
			PollIntervalMs: maxInt(100, pickInt(overridesBatchPoll(overrides), defaultsBatchPoll(defaults), 2000)),
			TimeoutMinutes: maxInt(1, pickInt(overridesBatchTimeout(overrides), defaultsBatchTimeout(defaults), 60)),
		}
	}

	modelDefault := ""
	switch provider {
	case "gemini":
		modelDefault = embedding.DefaultGeminiEmbeddingModel
	case "openai":
		modelDefault = embedding.DefaultOpenAIEmbeddingModel
	}
	model := pickString(overridesModel(overrides), defaultsModel(defaults), modelDefault)

	rawSources := mergeStringSlices(defaultsSources(defaults), overridesSources(overrides))
	sources := normalizeSources(rawSources, sessionMemory)

	rawExtraPaths := mergeStringSlices(defaultsExtraPaths(defaults), overridesExtraPaths(overrides))
	extraPaths := dedupeStrings(rawExtraPaths)

	vector := memory.VectorConfig{
		Enabled:       pickBool(overridesVectorEnabled(overrides), defaultsVectorEnabled(defaults), true),
		ExtensionPath: pickString(overridesVectorExtension(overrides), defaultsVectorExtension(defaults), ""),
	}

	store := memory.StoreConfig{
		Driver: "sqlite",
		Path:   "",
		Vector: vector,
	}

	chunkTokens := pickInt(overridesChunkTokens(overrides), defaultsChunkTokens(defaults), memory.DefaultChunkTokens)
	chunkOverlap := pickInt(overridesChunkOverlap(overrides), defaultsChunkOverlap(defaults), memory.DefaultChunkOverlap)
	if chunkTokens < 1 {
		chunkTokens = memory.DefaultChunkTokens
	}
	if chunkOverlap < 0 {
		chunkOverlap = 0
	}
	if chunkOverlap >= chunkTokens {
		chunkOverlap = maxInt(0, chunkTokens-1)
	}

	sync := memory.SyncConfig{
		OnSessionStart:  pickBool(overridesSyncOnStart(overrides), defaultsSyncOnStart(defaults), true),
		OnSearch:        pickBool(overridesSyncOnSearch(overrides), defaultsSyncOnSearch(defaults), true),
		Watch:           pickBool(overridesSyncWatch(overrides), defaultsSyncWatch(defaults), true),
		WatchDebounceMs: pickInt(overridesSyncWatchDebounce(overrides), defaultsSyncWatchDebounce(defaults), memory.DefaultWatchDebounceMs),
		IntervalMinutes: pickInt(overridesSyncInterval(overrides), defaultsSyncInterval(defaults), 0),
		Sessions: memory.SessionSyncConfig{
			DeltaBytes:    pickInt(overridesSyncDeltaBytes(overrides), defaultsSyncDeltaBytes(defaults), memory.DefaultSessionDeltaBytes),
			DeltaMessages: pickInt(overridesSyncDeltaMessages(overrides), defaultsSyncDeltaMessages(defaults), memory.DefaultSessionDeltaMessages),
		},
	}

	query := memory.QueryConfig{
		MaxResults: pickInt(overridesQueryMaxResults(overrides), defaultsQueryMaxResults(defaults), memory.DefaultMaxResults),
		MinScore:   pickFloat(overridesQueryMinScore(overrides), defaultsQueryMinScore(defaults), memory.DefaultMinScore),
		Hybrid: memory.HybridConfig{
			Enabled:             pickBool(overridesHybridEnabled(overrides), defaultsHybridEnabled(defaults), memory.DefaultHybridEnabled),
			VectorWeight:        pickFloat(overridesHybridVectorWeight(overrides), defaultsHybridVectorWeight(defaults), memory.DefaultHybridVectorWeight),
			TextWeight:          pickFloat(overridesHybridTextWeight(overrides), defaultsHybridTextWeight(defaults), memory.DefaultHybridTextWeight),
			CandidateMultiplier: pickInt(overridesHybridCandidateMultiplier(overrides), defaultsHybridCandidateMultiplier(defaults), memory.DefaultHybridCandidateMultiple),
		},
	}

	cache := memory.CacheConfig{
		Enabled:    pickBool(overridesCacheEnabled(overrides), defaultsCacheEnabled(defaults), memory.DefaultCacheEnabled),
		MaxEntries: pickInt(overridesCacheMaxEntries(overrides), defaultsCacheMaxEntries(defaults), 0),
	}

	query.MinScore = clampFloat(query.MinScore, 0, 1)
	vectorWeight := clampFloat(query.Hybrid.VectorWeight, 0, 1)
	textWeight := clampFloat(query.Hybrid.TextWeight, 0, 1)
	sum := vectorWeight + textWeight
	if sum <= 0 {
		query.Hybrid.VectorWeight = memory.DefaultHybridVectorWeight
		query.Hybrid.TextWeight = memory.DefaultHybridTextWeight
	} else {
		query.Hybrid.VectorWeight = vectorWeight / sum
		query.Hybrid.TextWeight = textWeight / sum
	}
	query.Hybrid.CandidateMultiplier = clampInt(query.Hybrid.CandidateMultiplier, 1, 20)
	sync.Sessions.DeltaBytes = maxInt(0, sync.Sessions.DeltaBytes)
	sync.Sessions.DeltaMessages = maxInt(0, sync.Sessions.DeltaMessages)
	if cache.MaxEntries <= 0 {
		cache.MaxEntries = 0
	}

	experimental := memory.ExperimentalConfig{SessionMemory: sessionMemory}

	resolved := &memory.ResolvedConfig{
		Enabled:      enabled,
		Sources:      sources,
		ExtraPaths:   extraPaths,
		Provider:     provider,
		Model:        model,
		Fallback:     fallback,
		Remote:       remote,
		Store:        store,
		Chunking:     memory.ChunkingConfig{Tokens: chunkTokens, Overlap: chunkOverlap},
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
		input = []string{memory.DefaultMemorySource}
	}
	normalized := make(map[string]bool)
	for _, source := range input {
		switch strings.ToLower(strings.TrimSpace(source)) {
		case "memory":
			normalized["memory"] = true
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

func mergeHeaders(base, override map[string]string) map[string]string {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	out := make(map[string]string)
	for k, v := range base {
		out[k] = v
	}
	for k, v := range override {
		out[k] = v
	}
	return out
}

func mergeStringSlices(a, b []string) []string {
	merged := make([]string, 0, len(a)+len(b))
	merged = append(merged, a...)
	merged = append(merged, b...)
	return merged
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

func clampFloat(value, min, max float64) float64 {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func clampInt(value, min, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func overridesEnabled(cfg *agents.MemorySearchConfig) *bool {
	if cfg == nil {
		return nil
	}
	return cfg.Enabled
}

func defaultsEnabled(cfg *MemorySearchConfig) *bool {
	if cfg == nil {
		return nil
	}
	return cfg.Enabled
}

func overridesSessionMemory(cfg *agents.MemorySearchConfig) *bool {
	if cfg == nil || cfg.Experimental == nil {
		return nil
	}
	return cfg.Experimental.SessionMemory
}

func defaultsSessionMemory(cfg *MemorySearchConfig) *bool {
	if cfg == nil || cfg.Experimental == nil {
		return nil
	}
	return cfg.Experimental.SessionMemory
}

func overridesProvider(cfg *agents.MemorySearchConfig) string {
	if cfg == nil {
		return ""
	}
	return cfg.Provider
}

func defaultsProvider(cfg *MemorySearchConfig) string {
	if cfg == nil {
		return ""
	}
	return cfg.Provider
}

func overridesFallback(cfg *agents.MemorySearchConfig) string {
	if cfg == nil {
		return ""
	}
	return cfg.Fallback
}

func defaultsFallback(cfg *MemorySearchConfig) string {
	if cfg == nil {
		return ""
	}
	return cfg.Fallback
}

func overridesModel(cfg *agents.MemorySearchConfig) string {
	if cfg == nil {
		return ""
	}
	return cfg.Model
}

func defaultsModel(cfg *MemorySearchConfig) string {
	if cfg == nil {
		return ""
	}
	return cfg.Model
}

func overridesSources(cfg *agents.MemorySearchConfig) []string {
	if cfg == nil {
		return nil
	}
	return cfg.Sources
}

func defaultsSources(cfg *MemorySearchConfig) []string {
	if cfg == nil {
		return nil
	}
	return cfg.Sources
}

func overridesExtraPaths(cfg *agents.MemorySearchConfig) []string {
	if cfg == nil {
		return nil
	}
	return cfg.ExtraPaths
}

func defaultsExtraPaths(cfg *MemorySearchConfig) []string {
	if cfg == nil {
		return nil
	}
	return cfg.ExtraPaths
}

func overridesVectorEnabled(cfg *agents.MemorySearchConfig) *bool {
	if cfg == nil || cfg.Store == nil || cfg.Store.Vector == nil {
		return nil
	}
	return cfg.Store.Vector.Enabled
}

func defaultsVectorEnabled(cfg *MemorySearchConfig) *bool {
	if cfg == nil || cfg.Store == nil || cfg.Store.Vector == nil {
		return nil
	}
	return cfg.Store.Vector.Enabled
}

func overridesVectorExtension(cfg *agents.MemorySearchConfig) string {
	if cfg == nil || cfg.Store == nil || cfg.Store.Vector == nil {
		return ""
	}
	return cfg.Store.Vector.ExtensionPath
}

func defaultsVectorExtension(cfg *MemorySearchConfig) string {
	if cfg == nil || cfg.Store == nil || cfg.Store.Vector == nil {
		return ""
	}
	return cfg.Store.Vector.ExtensionPath
}

func overridesChunkTokens(cfg *agents.MemorySearchConfig) int {
	if cfg == nil || cfg.Chunking == nil {
		return 0
	}
	return cfg.Chunking.Tokens
}

func defaultsChunkTokens(cfg *MemorySearchConfig) int {
	if cfg == nil || cfg.Chunking == nil {
		return 0
	}
	return cfg.Chunking.Tokens
}

func overridesChunkOverlap(cfg *agents.MemorySearchConfig) int {
	if cfg == nil || cfg.Chunking == nil {
		return 0
	}
	return cfg.Chunking.Overlap
}

func defaultsChunkOverlap(cfg *MemorySearchConfig) int {
	if cfg == nil || cfg.Chunking == nil {
		return 0
	}
	return cfg.Chunking.Overlap
}

func overridesSyncOnStart(cfg *agents.MemorySearchConfig) *bool {
	if cfg == nil || cfg.Sync == nil {
		return nil
	}
	return cfg.Sync.OnSessionStart
}

func defaultsSyncOnStart(cfg *MemorySearchConfig) *bool {
	if cfg == nil || cfg.Sync == nil {
		return nil
	}
	return cfg.Sync.OnSessionStart
}

func overridesSyncOnSearch(cfg *agents.MemorySearchConfig) *bool {
	if cfg == nil || cfg.Sync == nil {
		return nil
	}
	return cfg.Sync.OnSearch
}

func defaultsSyncOnSearch(cfg *MemorySearchConfig) *bool {
	if cfg == nil || cfg.Sync == nil {
		return nil
	}
	return cfg.Sync.OnSearch
}

func overridesSyncWatch(cfg *agents.MemorySearchConfig) *bool {
	if cfg == nil || cfg.Sync == nil {
		return nil
	}
	return cfg.Sync.Watch
}

func defaultsSyncWatch(cfg *MemorySearchConfig) *bool {
	if cfg == nil || cfg.Sync == nil {
		return nil
	}
	return cfg.Sync.Watch
}

func overridesSyncWatchDebounce(cfg *agents.MemorySearchConfig) int {
	if cfg == nil || cfg.Sync == nil {
		return 0
	}
	return cfg.Sync.WatchDebounceMs
}

func defaultsSyncWatchDebounce(cfg *MemorySearchConfig) int {
	if cfg == nil || cfg.Sync == nil {
		return 0
	}
	return cfg.Sync.WatchDebounceMs
}

func overridesSyncInterval(cfg *agents.MemorySearchConfig) int {
	if cfg == nil || cfg.Sync == nil {
		return 0
	}
	return cfg.Sync.IntervalMinutes
}

func defaultsSyncInterval(cfg *MemorySearchConfig) int {
	if cfg == nil || cfg.Sync == nil {
		return 0
	}
	return cfg.Sync.IntervalMinutes
}

func overridesSyncDeltaBytes(cfg *agents.MemorySearchConfig) int {
	if cfg == nil || cfg.Sync == nil || cfg.Sync.Sessions == nil {
		return 0
	}
	return cfg.Sync.Sessions.DeltaBytes
}

func defaultsSyncDeltaBytes(cfg *MemorySearchConfig) int {
	if cfg == nil || cfg.Sync == nil || cfg.Sync.Sessions == nil {
		return 0
	}
	return cfg.Sync.Sessions.DeltaBytes
}

func overridesSyncDeltaMessages(cfg *agents.MemorySearchConfig) int {
	if cfg == nil || cfg.Sync == nil || cfg.Sync.Sessions == nil {
		return 0
	}
	return cfg.Sync.Sessions.DeltaMessages
}

func defaultsSyncDeltaMessages(cfg *MemorySearchConfig) int {
	if cfg == nil || cfg.Sync == nil || cfg.Sync.Sessions == nil {
		return 0
	}
	return cfg.Sync.Sessions.DeltaMessages
}

func overridesQueryMaxResults(cfg *agents.MemorySearchConfig) int {
	if cfg == nil || cfg.Query == nil {
		return 0
	}
	return cfg.Query.MaxResults
}

func defaultsQueryMaxResults(cfg *MemorySearchConfig) int {
	if cfg == nil || cfg.Query == nil {
		return 0
	}
	return cfg.Query.MaxResults
}

func overridesQueryMinScore(cfg *agents.MemorySearchConfig) float64 {
	if cfg == nil || cfg.Query == nil {
		return 0
	}
	return cfg.Query.MinScore
}

func defaultsQueryMinScore(cfg *MemorySearchConfig) float64 {
	if cfg == nil || cfg.Query == nil {
		return 0
	}
	return cfg.Query.MinScore
}

func overridesHybridEnabled(cfg *agents.MemorySearchConfig) *bool {
	if cfg == nil || cfg.Query == nil || cfg.Query.Hybrid == nil {
		return nil
	}
	return cfg.Query.Hybrid.Enabled
}

func defaultsHybridEnabled(cfg *MemorySearchConfig) *bool {
	if cfg == nil || cfg.Query == nil || cfg.Query.Hybrid == nil {
		return nil
	}
	return cfg.Query.Hybrid.Enabled
}

func overridesHybridVectorWeight(cfg *agents.MemorySearchConfig) float64 {
	if cfg == nil || cfg.Query == nil || cfg.Query.Hybrid == nil {
		return 0
	}
	return cfg.Query.Hybrid.VectorWeight
}

func defaultsHybridVectorWeight(cfg *MemorySearchConfig) float64 {
	if cfg == nil || cfg.Query == nil || cfg.Query.Hybrid == nil {
		return 0
	}
	return cfg.Query.Hybrid.VectorWeight
}

func overridesHybridTextWeight(cfg *agents.MemorySearchConfig) float64 {
	if cfg == nil || cfg.Query == nil || cfg.Query.Hybrid == nil {
		return 0
	}
	return cfg.Query.Hybrid.TextWeight
}

func defaultsHybridTextWeight(cfg *MemorySearchConfig) float64 {
	if cfg == nil || cfg.Query == nil || cfg.Query.Hybrid == nil {
		return 0
	}
	return cfg.Query.Hybrid.TextWeight
}

func overridesHybridCandidateMultiplier(cfg *agents.MemorySearchConfig) int {
	if cfg == nil || cfg.Query == nil || cfg.Query.Hybrid == nil {
		return 0
	}
	return cfg.Query.Hybrid.CandidateMultiplier
}

func defaultsHybridCandidateMultiplier(cfg *MemorySearchConfig) int {
	if cfg == nil || cfg.Query == nil || cfg.Query.Hybrid == nil {
		return 0
	}
	return cfg.Query.Hybrid.CandidateMultiplier
}

func overridesCacheEnabled(cfg *agents.MemorySearchConfig) *bool {
	if cfg == nil || cfg.Cache == nil {
		return nil
	}
	return cfg.Cache.Enabled
}

func defaultsCacheEnabled(cfg *MemorySearchConfig) *bool {
	if cfg == nil || cfg.Cache == nil {
		return nil
	}
	return cfg.Cache.Enabled
}

func overridesCacheMaxEntries(cfg *agents.MemorySearchConfig) int {
	if cfg == nil || cfg.Cache == nil {
		return 0
	}
	return cfg.Cache.MaxEntries
}

func defaultsCacheMaxEntries(cfg *MemorySearchConfig) int {
	if cfg == nil || cfg.Cache == nil {
		return 0
	}
	return cfg.Cache.MaxEntries
}

func overridesRemoteBaseURL(cfg *agents.MemorySearchConfig) string {
	if cfg == nil || cfg.Remote == nil {
		return ""
	}
	return cfg.Remote.BaseURL
}

func defaultsRemoteBaseURL(cfg *MemorySearchConfig) string {
	if cfg == nil || cfg.Remote == nil {
		return ""
	}
	return cfg.Remote.BaseURL
}

func overridesRemoteAPIKey(cfg *agents.MemorySearchConfig) string {
	if cfg == nil || cfg.Remote == nil {
		return ""
	}
	return cfg.Remote.APIKey
}

func defaultsRemoteAPIKey(cfg *MemorySearchConfig) string {
	if cfg == nil || cfg.Remote == nil {
		return ""
	}
	return cfg.Remote.APIKey
}

func overridesRemoteHeaders(cfg *agents.MemorySearchConfig) map[string]string {
	if cfg == nil || cfg.Remote == nil {
		return nil
	}
	return cfg.Remote.Headers
}

func defaultsRemoteHeaders(cfg *MemorySearchConfig) map[string]string {
	if cfg == nil || cfg.Remote == nil {
		return nil
	}
	return cfg.Remote.Headers
}

func hasRemoteOverrides(cfg *agents.MemorySearchConfig) bool {
	if cfg == nil || cfg.Remote == nil {
		return false
	}
	return cfg.Remote.BaseURL != "" || cfg.Remote.APIKey != "" || len(cfg.Remote.Headers) > 0
}

func hasRemoteDefaults(cfg *MemorySearchConfig) bool {
	if cfg == nil || cfg.Remote == nil {
		return false
	}
	return cfg.Remote.BaseURL != "" || cfg.Remote.APIKey != "" || len(cfg.Remote.Headers) > 0
}

func overridesBatchEnabled(cfg *agents.MemorySearchConfig) *bool {
	if cfg == nil || cfg.Remote == nil || cfg.Remote.Batch == nil {
		return nil
	}
	return cfg.Remote.Batch.Enabled
}

func defaultsBatchEnabled(cfg *MemorySearchConfig) *bool {
	if cfg == nil || cfg.Remote == nil || cfg.Remote.Batch == nil {
		return nil
	}
	return cfg.Remote.Batch.Enabled
}

func overridesBatchWait(cfg *agents.MemorySearchConfig) *bool {
	if cfg == nil || cfg.Remote == nil || cfg.Remote.Batch == nil {
		return nil
	}
	return cfg.Remote.Batch.Wait
}

func defaultsBatchWait(cfg *MemorySearchConfig) *bool {
	if cfg == nil || cfg.Remote == nil || cfg.Remote.Batch == nil {
		return nil
	}
	return cfg.Remote.Batch.Wait
}

func overridesBatchConcurrency(cfg *agents.MemorySearchConfig) int {
	if cfg == nil || cfg.Remote == nil || cfg.Remote.Batch == nil {
		return 0
	}
	return cfg.Remote.Batch.Concurrency
}

func defaultsBatchConcurrency(cfg *MemorySearchConfig) int {
	if cfg == nil || cfg.Remote == nil || cfg.Remote.Batch == nil {
		return 0
	}
	return cfg.Remote.Batch.Concurrency
}

func overridesBatchPoll(cfg *agents.MemorySearchConfig) int {
	if cfg == nil || cfg.Remote == nil || cfg.Remote.Batch == nil {
		return 0
	}
	return cfg.Remote.Batch.PollIntervalMs
}

func defaultsBatchPoll(cfg *MemorySearchConfig) int {
	if cfg == nil || cfg.Remote == nil || cfg.Remote.Batch == nil {
		return 0
	}
	return cfg.Remote.Batch.PollIntervalMs
}

func overridesBatchTimeout(cfg *agents.MemorySearchConfig) int {
	if cfg == nil || cfg.Remote == nil || cfg.Remote.Batch == nil {
		return 0
	}
	return cfg.Remote.Batch.TimeoutMinutes
}

func defaultsBatchTimeout(cfg *MemorySearchConfig) int {
	if cfg == nil || cfg.Remote == nil || cfg.Remote.Batch == nil {
		return 0
	}
	return cfg.Remote.Batch.TimeoutMinutes
}
