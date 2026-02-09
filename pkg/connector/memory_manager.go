package connector

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/util/dbutil"

	"github.com/beeper/ai-bridge/pkg/memory"
	"github.com/beeper/ai-bridge/pkg/textfs"
)

const memorySnippetMaxChars = 700

var keywordTokenRE = regexp.MustCompile(`[A-Za-z0-9_]+`)

type MemorySearchManager struct {
	client       *AIClient
	db           *dbutil.Database
	bridgeID     string
	loginID      string
	agentID      string
	cfg          *memory.ResolvedConfig
	provider     memory.EmbeddingProvider
	status       memory.ProviderStatus
	providerKey  string
	vectorDims   int
	indexGen     string
	ftsAvailable bool
	ftsError     string
	log          zerolog.Logger

	dirty             bool
	sessionsDirty     bool
	syncProgress      func(completed, total int, label string)
	sessionWarm       map[string]struct{}
	watchTimer        *time.Timer
	sessionWatchTimer *time.Timer
	sessionWatchKey   string
	intervalOnce      sync.Once
	intervalStop      chan struct{}
	intervalStopOnce  sync.Once
	vectorExtOK       *vectorExtStatus
	vectorError       string
	batchEnabled      bool
	batchFailures     int
	batchLastError    string
	batchLastProvider string
	mu                sync.Mutex
}

type MemorySearchStatus struct {
	Files             int
	Chunks            int
	Dirty             bool
	WorkspaceDir      string
	DBPath            string
	Provider          string
	Model             string
	RequestedProvider string
	Sources           []string
	ExtraPaths        []string
	SourceCounts      []MemorySearchSourceCount
	Cache             *MemorySearchCacheStatus
	FTS               *MemorySearchFTSStatus
	Fallback          *memory.FallbackStatus
	Vector            *MemorySearchVectorStatus
	Batch             *MemorySearchBatchStatus
}

type MemorySearchSourceCount struct {
	Source string
	Files  int
	Chunks int
}

type MemorySearchCacheStatus struct {
	Enabled    bool
	Entries    int
	MaxEntries int
}

type MemorySearchFTSStatus struct {
	Enabled   bool
	Available bool
	Error     string
}

type MemorySearchVectorStatus struct {
	Enabled       bool
	Available     *bool
	ExtensionPath string
	LoadError     string
	Dims          int
}

type MemorySearchBatchStatus struct {
	Enabled        bool
	Failures       int
	Limit          int
	Wait           bool
	Concurrency    int
	PollIntervalMs int
	TimeoutMs      int
	LastError      string
	LastProvider   string
}

var memoryManagerCache = struct {
	mu       sync.Mutex
	managers map[string]*MemorySearchManager
}{
	managers: make(map[string]*MemorySearchManager),
}

func getMemorySearchManager(client *AIClient, agentID string) (*MemorySearchManager, string) {
	if client == nil || client.connector == nil {
		return nil, "memory search unavailable"
	}
	cfg, err := resolveMemorySearchConfig(client, agentID)
	if err != nil || cfg == nil {
		if err != nil {
			return nil, err.Error()
		}
		return nil, "memory search disabled"
	}

	bridgeID := string(client.UserLogin.Bridge.DB.BridgeID)
	loginID := string(client.UserLogin.ID)
	if agentID == "" {
		agentID = "default"
	}

	cacheKey := memoryManagerCacheKey(bridgeID, loginID, agentID, cfg)

	memoryManagerCache.mu.Lock()
	defer memoryManagerCache.mu.Unlock()
	if existing := memoryManagerCache.managers[cacheKey]; existing != nil {
		return existing, ""
	}

	providerResult, err := buildMemoryProvider(client, cfg)
	if err != nil {
		return nil, err.Error()
	}

	manager := &MemorySearchManager{
		client:      client,
		db:          client.UserLogin.Bridge.DB.Database,
		bridgeID:    bridgeID,
		loginID:     loginID,
		agentID:     agentID,
		cfg:         cfg,
		provider:    providerResult.Provider,
		status:      providerResult.Status,
		providerKey: providerResult.ProviderKey,
		log:         client.log.With().Str("component", "memory").Logger(),
	}
	if hasSource(cfg.Sources, "memory") || hasSource(cfg.Sources, "workspace") {
		manager.dirty = true
	}
	manager.batchEnabled = cfg.Remote.Batch.Enabled

	manager.ensureSchema(context.Background())
	manager.ensureDefaultMemoryFiles(context.Background())
	manager.ensureIntervalSync()
	memoryManagerCache.managers[cacheKey] = manager
	return manager, ""
}

func (m *MemorySearchManager) Status() memory.ProviderStatus {
	return m.status
}

func (m *MemorySearchManager) ensureDefaultMemoryFiles(ctx context.Context) {
	if m == nil || m.db == nil || m.cfg == nil {
		return
	}
	if !hasSource(m.cfg.Sources, "memory") {
		return
	}
	store := textfs.NewStore(m.db, m.bridgeID, m.loginID, m.agentID)
	_, _ = store.WriteIfMissing(ctx, "MEMORY.md", "")
}

func (m *MemorySearchManager) ProbeVectorAvailability(ctx context.Context) bool {
	if m == nil || m.cfg == nil || !m.cfg.Store.Vector.Enabled {
		return false
	}
	// Probe by trying to grab+release a vector connection.
	err := m.withVectorConn(ctx, func(_ *sql.Conn) error { return nil })
	return err == nil
}

func (m *MemorySearchManager) ProbeEmbeddingAvailability(ctx context.Context) (bool, string) {
	if m == nil || m.provider == nil {
		return false, "memory search unavailable"
	}
	_, err := m.embedBatchWithRetry(ctx, []string{"ping"})
	if err != nil {
		return false, err.Error()
	}
	return true, ""
}

func (m *MemorySearchManager) StatusDetails(ctx context.Context) (*MemorySearchStatus, error) {
	if m == nil {
		return nil, errors.New("memory search unavailable")
	}
	workspaceDir := resolvePromptWorkspaceDir()
	status := &MemorySearchStatus{
		Dirty:             m.dirty,
		WorkspaceDir:      workspaceDir,
		DBPath:            resolveMemoryDBPath(m.cfg, m.agentID),
		Provider:          m.status.Provider,
		Model:             m.status.Model,
		RequestedProvider: m.cfg.Provider,
		Sources:           slices.Clone(m.cfg.Sources),
		ExtraPaths:        resolveStatusExtraPaths(m.cfg.ExtraPaths, workspaceDir),
		Fallback:          m.status.Fallback,
	}

	genSQL, genArgs := generationFilterSQL(5, m.indexGen)
	sourceSQL, sourceArgs := sourceFilterSQL(4, m.cfg.Sources)
	chunkArgs := []any{m.bridgeID, m.loginID, m.agentID}
	chunkArgs = append(chunkArgs, sourceArgs...)
	chunkArgs = append(chunkArgs, genArgs...)
	row := m.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM ai_memory_chunks
         WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3`+sourceSQL+genSQL,
		chunkArgs...,
	)
	_ = row.Scan(&status.Chunks)

	files := 0
	if hasSource(m.cfg.Sources, "memory") {
		row = m.db.QueryRow(ctx,
			`SELECT COUNT(*) FROM ai_memory_files WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3 AND source=$4`,
			m.bridgeID, m.loginID, m.agentID, "memory",
		)
		var count int
		_ = row.Scan(&count)
		files += count
	}
	if hasSource(m.cfg.Sources, "workspace") {
		row = m.db.QueryRow(ctx,
			`SELECT COUNT(*) FROM ai_memory_files WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3 AND source=$4`,
			m.bridgeID, m.loginID, m.agentID, "workspace",
		)
		var count int
		_ = row.Scan(&count)
		files += count
	}
	if hasSource(m.cfg.Sources, "sessions") {
		row = m.db.QueryRow(ctx,
			`SELECT COUNT(*) FROM ai_memory_session_files WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3`,
			m.bridgeID, m.loginID, m.agentID,
		)
		var count int
		_ = row.Scan(&count)
		files += count
	}
	status.Files = files

	status.SourceCounts = buildSourceCounts(ctx, m)

	cacheStatus := &MemorySearchCacheStatus{Enabled: m.cfg.Cache.Enabled, MaxEntries: m.cfg.Cache.MaxEntries}
	if m.cfg.Cache.Enabled {
		row := m.db.QueryRow(ctx,
			`SELECT COUNT(*) FROM ai_memory_embedding_cache
             WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3`,
			m.bridgeID, m.loginID, m.agentID,
		)
		_ = row.Scan(&cacheStatus.Entries)
	}
	status.Cache = cacheStatus

	status.FTS = &MemorySearchFTSStatus{
		Enabled:   m.cfg.Query.Hybrid.Enabled,
		Available: m.ftsAvailable,
		Error:     m.ftsError,
	}

	vectorAvailablePtr := (*bool)(nil)
	if m.vectorAvailable() {
		ready := true
		vectorAvailablePtr = &ready
	} else if m.vectorError != "" {
		ready := false
		vectorAvailablePtr = &ready
	}
	status.Vector = &MemorySearchVectorStatus{
		Enabled:       m.cfg.Store.Vector.Enabled,
		Available:     vectorAvailablePtr,
		ExtensionPath: m.cfg.Store.Vector.ExtensionPath,
		LoadError:     m.vectorError,
		Dims:          m.vectorDims,
	}

	timeoutMs := m.cfg.Remote.Batch.TimeoutMinutes * 60 * 1000
	status.Batch = &MemorySearchBatchStatus{
		Enabled:        m.batchEnabled,
		Failures:       m.batchFailures,
		Limit:          2,
		Wait:           m.cfg.Remote.Batch.Wait,
		Concurrency:    m.cfg.Remote.Batch.Concurrency,
		PollIntervalMs: m.cfg.Remote.Batch.PollIntervalMs,
		TimeoutMs:      timeoutMs,
		LastError:      m.batchLastError,
		LastProvider:   m.batchLastProvider,
	}

	return status, nil
}

func buildSourceCounts(ctx context.Context, m *MemorySearchManager) []MemorySearchSourceCount {
	if m == nil {
		return nil
	}
	out := make([]MemorySearchSourceCount, 0, len(m.cfg.Sources))
	for _, source := range m.cfg.Sources {
		count := MemorySearchSourceCount{Source: source}
		switch source {
		case "memory":
			row := m.db.QueryRow(ctx,
				`SELECT COUNT(*) FROM ai_memory_files WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3 AND source=$4`,
				m.bridgeID, m.loginID, m.agentID, "memory",
			)
			_ = row.Scan(&count.Files)
		case "workspace":
			row := m.db.QueryRow(ctx,
				`SELECT COUNT(*) FROM ai_memory_files WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3 AND source=$4`,
				m.bridgeID, m.loginID, m.agentID, "workspace",
			)
			_ = row.Scan(&count.Files)
		case "sessions":
			row := m.db.QueryRow(ctx,
				`SELECT COUNT(*) FROM ai_memory_session_files WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3`,
				m.bridgeID, m.loginID, m.agentID,
			)
			_ = row.Scan(&count.Files)
		}
		genSQL, genArgs := generationFilterSQL(5, m.indexGen)
		args := []any{m.bridgeID, m.loginID, m.agentID, source}
		args = append(args, genArgs...)
		row := m.db.QueryRow(ctx,
			`SELECT COUNT(*) FROM ai_memory_chunks WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3 AND source=$4`+genSQL,
			args...,
		)
		_ = row.Scan(&count.Chunks)
		out = append(out, count)
	}
	return out
}

func (m *MemorySearchManager) Search(ctx context.Context, query string, opts memory.SearchOptions) ([]memory.SearchResult, error) {
	if m == nil {
		return nil, errors.New("memory search unavailable")
	}
	m.warmSession(ctx, opts.SessionKey)
	if m.cfg.Sync.OnSearch {
		m.mu.Lock()
		shouldSync := m.dirty || m.sessionsDirty
		m.mu.Unlock()
		if shouldSync {
			go func(sessionKey string) {
				if err := m.sync(context.Background(), sessionKey, false); err != nil {
					m.log.Warn().Msg("memory sync failed (search): " + err.Error())
				}
			}(opts.SessionKey)
		}
	}

	mode := strings.ToLower(strings.TrimSpace(opts.Mode))
	switch mode {
	case "", "auto", "semantic", "keyword", "hybrid", "list":
	default:
		mode = "auto"
	}

	cleaned := strings.TrimSpace(query)
	if mode != "list" && cleaned == "" {
		return []memory.SearchResult{}, nil
	}

	maxResults := m.cfg.Query.MaxResults
	if opts.MaxResults > 0 {
		maxResults = opts.MaxResults
	}
	if maxResults <= 0 {
		maxResults = memory.DefaultMaxResults
	}

	minScore := m.cfg.Query.MinScore
	if !math.IsNaN(opts.MinScore) {
		minScore = opts.MinScore
	}

	candidates := maxResults
	if m.cfg.Query.Hybrid.CandidateMultiplier > 0 {
		candidates = maxResults * m.cfg.Query.Hybrid.CandidateMultiplier
	}
	if candidates < 1 {
		candidates = 1
	}
	if candidates > 200 {
		candidates = 200
	}

	sources := normalizeSearchSources(opts.Sources, m.cfg.Sources)
	pathPrefix := normalizeSearchPathPrefix(opts.PathPrefix)

	if mode == "list" {
		results, err := m.listRecentFiles(ctx, sources, pathPrefix, maxResults)
		if err != nil {
			return nil, err
		}
		return clampInjectedChars(filterAndLimit(results, minScore, maxResults), m.cfg.Query.MaxInjectedChars), nil
	}

	wantKeyword := mode == "auto" || mode == "keyword" || mode == "hybrid"
	wantVector := mode == "auto" || mode == "semantic" || mode == "hybrid"

	keywordResults := []memory.HybridKeywordResult{}
	keywordOK := false
	if wantKeyword {
		if m.ftsAvailable {
			results, err := m.searchKeyword(ctx, cleaned, candidates, sources, pathPrefix)
			if err == nil {
				keywordResults = results
				keywordOK = true
			}
		}
		if !keywordOK {
			results, err := m.searchKeywordScan(ctx, cleaned, candidates, sources, pathPrefix)
			if err == nil {
				keywordResults = results
				keywordOK = true
			}
		}
	}

	vectorResults := []memory.HybridVectorResult{}
	vectorOK := false
	if wantVector && m.cfg.Store.Vector.Enabled {
		queryVec, err := m.embedQueryWithTimeout(ctx, cleaned)
		if err != nil {
			// For semantic-only queries, embedding failures are fatal. For auto/hybrid,
			// we degrade to keyword search.
			if mode == "semantic" {
				return nil, err
			}
			queryVec = nil
		}
		if len(queryVec) > 0 {
			hasVector := false
			for _, v := range queryVec {
				if v != 0 {
					hasVector = true
					break
				}
			}
			if hasVector {
				results, err := m.searchVector(ctx, queryVec, candidates, sources, pathPrefix)
				if err == nil {
					vectorResults = results
					vectorOK = true
				}
			}
		}
	}

	switch mode {
	case "semantic":
		return clampInjectedChars(filterAndLimit(vectorResultsToSearch(vectorResults), minScore, maxResults), m.cfg.Query.MaxInjectedChars), nil
	case "keyword":
		return clampInjectedChars(filterAndLimit(keywordResultsToSearch(keywordResults), minScore, maxResults), m.cfg.Query.MaxInjectedChars), nil
	}

	// auto/hybrid: merge when hybrid is enabled and we have both sides; otherwise
	// degrade to whichever side succeeded.
	if m.cfg.Query.Hybrid.Enabled && vectorOK && keywordOK {
		merged := memory.MergeHybridResults(vectorResults, keywordResults, m.cfg.Query.Hybrid.VectorWeight, m.cfg.Query.Hybrid.TextWeight)
		return clampInjectedChars(filterAndLimit(merged, minScore, maxResults), m.cfg.Query.MaxInjectedChars), nil
	}
	if vectorOK {
		return clampInjectedChars(filterAndLimit(vectorResultsToSearch(vectorResults), minScore, maxResults), m.cfg.Query.MaxInjectedChars), nil
	}
	if keywordOK {
		return clampInjectedChars(filterAndLimit(keywordResultsToSearch(keywordResults), minScore, maxResults), m.cfg.Query.MaxInjectedChars), nil
	}
	return []memory.SearchResult{}, nil
}

func normalizeSearchSources(requested []string, fallback []string) []string {
	if len(requested) == 0 {
		return slices.Clone(fallback)
	}
	seen := make(map[string]struct{})
	out := make([]string, 0, len(requested))
	for _, raw := range requested {
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case "memory", "workspace", "sessions":
			key := strings.ToLower(strings.TrimSpace(raw))
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, key)
		}
	}
	if len(out) == 0 {
		return slices.Clone(fallback)
	}
	return out
}

func normalizeSearchPathPrefix(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	normalized, err := textfs.NormalizePath(trimmed)
	if err != nil {
		return ""
	}
	return normalized
}

func (m *MemorySearchManager) listRecentFiles(ctx context.Context, sources []string, pathPrefix string, limit int) ([]memory.SearchResult, error) {
	if m == nil || m.db == nil {
		return nil, errors.New("memory search unavailable")
	}
	if limit <= 0 {
		limit = memory.DefaultMaxResults
	}
	if limit > 200 {
		limit = 200
	}

	baseArgs := []any{m.bridgeID, m.loginID, m.agentID}
	sourceSQL, sourceArgs := sourceFilterSQL(4, sources)
	pathSQL, pathArgs := pathPrefixFilterSQL(4+len(sourceArgs), pathPrefix)
	args := append(baseArgs, sourceArgs...)
	args = append(args, pathArgs...)
	args = append(args, limit)

	rows, err := m.db.Query(ctx,
		`SELECT path, source, content
         FROM ai_memory_files
         WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3`+sourceSQL+pathSQL+`
         ORDER BY updated_at DESC
         LIMIT $`+fmt.Sprintf("%d", len(args)),
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := make([]memory.SearchResult, 0, limit)
	for rows.Next() {
		var path, source, content string
		if err := rows.Scan(&path, &source, &content); err != nil {
			return nil, err
		}
		results = append(results, memory.SearchResult{
			Path:      path,
			StartLine: 1,
			EndLine:   1,
			Score:     1,
			Snippet:   truncateSnippet(normalizeNewlines(content)),
			Source:    source,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

func (m *MemorySearchManager) searchKeywordScan(ctx context.Context, query string, limit int, sources []string, pathPrefix string) ([]memory.HybridKeywordResult, error) {
	if m == nil || m.db == nil || limit <= 0 {
		return nil, nil
	}
	tokens := keywordTokenRE.FindAllString(query, -1)
	if len(tokens) == 0 {
		return nil, nil
	}
	for i, t := range tokens {
		tokens[i] = strings.ToLower(strings.TrimSpace(t))
	}

	// Scan more rows than we return so we can rank matches in-process.
	scanLimit := limit * 10
	if scanLimit < 200 {
		scanLimit = 200
	}
	if scanLimit > 1000 {
		scanLimit = 1000
	}

	baseArgs := []any{m.bridgeID, m.loginID, m.agentID, m.status.Model}
	sourceSQL, sourceArgs := sourceFilterSQL(5, sources)
	genSQL, genArgs := generationFilterSQL(5+len(sourceArgs), m.indexGen)
	pathSQL, pathArgs := pathPrefixFilterSQL(5+len(sourceArgs)+len(genArgs), pathPrefix)
	args := append(baseArgs, sourceArgs...)
	args = append(args, genArgs...)
	args = append(args, pathArgs...)

	// Add a cheap SQL prefilter: all tokens must appear somewhere.
	whereParts := make([]string, 0, len(tokens))
	for i, token := range tokens {
		whereParts = append(whereParts, fmt.Sprintf(" AND LOWER(text) LIKE $%d", 5+len(sourceArgs)+len(genArgs)+len(pathArgs)+i))
		args = append(args, "%"+token+"%")
	}
	args = append(args, scanLimit)

	rows, err := m.db.Query(ctx,
		`SELECT id, path, source, start_line, end_line, text
         FROM ai_memory_chunks
         WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3 AND model=$4`+sourceSQL+genSQL+pathSQL+strings.Join(whereParts, "")+`
         ORDER BY updated_at DESC
         LIMIT $`+fmt.Sprintf("%d", len(args)),
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type scored struct {
		r     memory.HybridKeywordResult
		score float64
	}
	scoredResults := make([]scored, 0, scanLimit)
	for rows.Next() {
		var id, path, source, text string
		var startLine, endLine int
		if err := rows.Scan(&id, &path, &source, &startLine, &endLine, &text); err != nil {
			return nil, err
		}
		lower := strings.ToLower(text)
		hits := 0
		for _, token := range tokens {
			if strings.Contains(lower, token) {
				hits++
			}
		}
		if hits == 0 {
			continue
		}
		score := float64(hits) / float64(len(tokens))
		scoredResults = append(scoredResults, scored{
			r: memory.HybridKeywordResult{
				ID:        id,
				Path:      path,
				StartLine: startLine,
				EndLine:   endLine,
				Source:    source,
				Snippet:   truncateSnippet(text),
				TextScore: score,
			},
			score: score,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	slices.SortFunc(scoredResults, func(a, b scored) int {
		if a.score == b.score {
			return 0
		}
		if a.score > b.score {
			return -1
		}
		return 1
	})
	if len(scoredResults) > limit {
		scoredResults = scoredResults[:limit]
	}
	out := make([]memory.HybridKeywordResult, 0, len(scoredResults))
	for _, entry := range scoredResults {
		out = append(out, entry.r)
	}
	return out, nil
}

func (m *MemorySearchManager) ReadFile(ctx context.Context, relPath string, from, lines *int) (map[string]any, error) {
	if m == nil {
		return nil, errors.New("memory search unavailable")
	}
	path, err := textfs.NormalizePath(relPath)
	if err != nil {
		return nil, errors.New("path required")
	}
	if !strings.HasSuffix(strings.ToLower(path), ".md") {
		return nil, errors.New("path required")
	}
	if !isAllowedMemoryPath(path, m.cfg.ExtraPaths) {
		return nil, errors.New("path required")
	}

	store := textfs.NewStore(m.db, m.bridgeID, m.loginID, m.agentID)
	entry, found, err := store.Read(ctx, path)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, errors.New("file not found")
	}

	content := normalizeNewlines(entry.Content)
	if from == nil && lines == nil {
		return map[string]any{"path": entry.Path, "text": content}, nil
	}
	lineList := strings.Split(content, "\n")
	start := 1
	if from != nil && *from > 1 {
		start = *from
	}
	count := len(lineList)
	if lines != nil {
		if *lines > 0 {
			count = *lines
		} else {
			count = 1
		}
	}
	if start < 1 {
		start = 1
	}
	end := start - 1 + count
	if end > len(lineList) {
		end = len(lineList)
	}
	if start > len(lineList) {
		return map[string]any{"path": entry.Path, "text": ""}, nil
	}
	slice := lineList[start-1 : end]
	return map[string]any{"path": entry.Path, "text": strings.Join(slice, "\n")}, nil
}

func filterAndLimit(results []memory.SearchResult, minScore float64, maxResults int) []memory.SearchResult {
	filtered := results[:0]
	for _, result := range results {
		if result.Score >= minScore {
			filtered = append(filtered, result)
		}
	}
	if len(filtered) > maxResults {
		return filtered[:maxResults]
	}
	return filtered
}

// clampInjectedChars enforces a total character budget across all search result snippets.
// If maxChars <= 0, no clamping is applied. The last result that would exceed the budget
// is truncated; subsequent results are dropped.
func clampInjectedChars(results []memory.SearchResult, maxChars int) []memory.SearchResult {
	if maxChars <= 0 || len(results) == 0 {
		return results
	}
	total := 0
	for i, result := range results {
		n := len(result.Snippet)
		if total+n > maxChars {
			remaining := maxChars - total
			if remaining <= 0 {
				return results[:i]
			}
			results[i].Snippet = result.Snippet[:remaining]
			return results[:i+1]
		}
		total += n
	}
	return results
}

func vectorResultsToSearch(results []memory.HybridVectorResult) []memory.SearchResult {
	out := make([]memory.SearchResult, 0, len(results))
	for _, entry := range results {
		out = append(out, memory.SearchResult{
			Path:      entry.Path,
			StartLine: entry.StartLine,
			EndLine:   entry.EndLine,
			Score:     entry.VectorScore,
			Snippet:   entry.Snippet,
			Source:    entry.Source,
		})
	}
	return out
}

func keywordResultsToSearch(results []memory.HybridKeywordResult) []memory.SearchResult {
	out := make([]memory.SearchResult, 0, len(results))
	for _, entry := range results {
		out = append(out, memory.SearchResult{
			Path:      entry.Path,
			StartLine: entry.StartLine,
			EndLine:   entry.EndLine,
			Score:     entry.TextScore,
			Snippet:   entry.Snippet,
			Source:    entry.Source,
		})
	}
	return out
}

func memoryManagerCacheKey(bridgeID, loginID, agentID string, cfg *memory.ResolvedConfig) string {
	if cfg == nil {
		return fmt.Sprintf("%s:%s:%s", bridgeID, loginID, agentID)
	}
	sources := slices.Clone(cfg.Sources)
	extra := slices.Clone(cfg.ExtraPaths)
	slices.Sort(sources)
	slices.Sort(extra)
	payload := map[string]any{
		"sources":       sources,
		"extraPaths":    extra,
		"provider":      cfg.Provider,
		"model":         cfg.Model,
		"fallback":      cfg.Fallback,
		"remoteBase":    cfg.Remote.BaseURL,
		"remoteHeaders": sortedHeaderNames(cfg.Remote.Headers),
		"remoteBatch": map[string]any{
			"enabled":        cfg.Remote.Batch.Enabled,
			"wait":           cfg.Remote.Batch.Wait,
			"concurrency":    cfg.Remote.Batch.Concurrency,
			"poll":           cfg.Remote.Batch.PollIntervalMs,
			"timeoutMinutes": cfg.Remote.Batch.TimeoutMinutes,
		},
		"remoteKey": hashString(cfg.Remote.APIKey),
		"store": map[string]any{
			"driver":        cfg.Store.Driver,
			"path":          cfg.Store.Path,
			"vectorEnabled": cfg.Store.Vector.Enabled,
			"vectorExt":     cfg.Store.Vector.ExtensionPath,
		},
		"chunking":     cfg.Chunking,
		"sync":         cfg.Sync,
		"query":        cfg.Query,
		"cache":        cfg.Cache,
		"experimental": cfg.Experimental,
	}
	raw, _ := json.Marshal(payload)
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("%s:%s:%s:%s", bridgeID, loginID, agentID, hex.EncodeToString(sum[:]))
}

func sortedHeaderNames(headers map[string]string) []string {
	if len(headers) == 0 {
		return nil
	}
	keys := make([]string, 0, len(headers))
	for key := range headers {
		trimmed := strings.ToLower(strings.TrimSpace(key))
		if trimmed == "" {
			continue
		}
		keys = append(keys, trimmed)
	}
	slices.Sort(keys)
	return keys
}

func hashString(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(trimmed))
	return hex.EncodeToString(sum[:])
}

func normalizeNewlines(text string) string {
	if text == "" {
		return ""
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return text
}

func truncateSnippet(text string) string {
	if text == "" {
		return ""
	}
	limit := memorySnippetMaxChars
	count := 0
	for _, r := range text {
		if r <= 0xFFFF {
			count++
		} else {
			count += 2
		}
		if count > limit {
			break
		}
	}
	if count <= limit {
		return text
	}
	out := make([]rune, 0, len(text))
	count = 0
	for _, r := range text {
		inc := 1
		if r > 0xFFFF {
			inc = 2
		}
		if count+inc > limit {
			break
		}
		out = append(out, r)
		count += inc
	}
	return string(out)
}

func isAllowedMemoryPath(path string, extraPaths []string) bool {
	// Memory search indexes markdown notes across the virtual workspace.
	// Callers already normalize paths; we still guard by suffix to avoid unnecessary sync triggers.
	if strings.HasSuffix(strings.ToLower(strings.TrimSpace(path)), ".md") {
		return true
	}
	if len(extraPaths) == 0 {
		return false
	}
	normalizedExtra := normalizeExtraPaths(extraPaths)
	for _, extra := range normalizedExtra {
		if strings.HasSuffix(strings.ToLower(extra), ".md") {
			if strings.EqualFold(path, extra) {
				return true
			}
			continue
		}
		if path == extra || strings.HasPrefix(path, extra+"/") {
			return true
		}
	}
	return false
}

func normalizeExtraPaths(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	seen := make(map[string]struct{})
	out := make([]string, 0, len(paths))
	for _, raw := range paths {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		normalized, err := textfs.NormalizePath(trimmed)
		if err != nil {
			continue
		}
		normalized = strings.TrimSuffix(normalized, "/")
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	slices.Sort(out)
	return out
}

func resolveStatusExtraPaths(paths []string, workspaceDir string) []string {
	if len(paths) == 0 {
		return nil
	}
	seen := make(map[string]struct{})
	out := make([]string, 0, len(paths))
	for _, raw := range paths {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		candidate := trimmed
		if !filepath.IsAbs(candidate) && strings.TrimSpace(workspaceDir) != "" {
			candidate = filepath.Join(workspaceDir, candidate)
		}
		cleaned := filepath.Clean(candidate)
		if _, ok := seen[cleaned]; ok {
			continue
		}
		seen[cleaned] = struct{}{}
		out = append(out, cleaned)
	}
	slices.Sort(out)
	return out
}

func resolveMemoryDBPath(_ *memory.ResolvedConfig, _ string) string {
	return "bridge.sqlite (vfs)"
}
