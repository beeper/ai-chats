package memory

import (
	"context"
	"crypto/sha256"
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

	memorycore "github.com/beeper/ai-bridge/pkg/memory"
	"github.com/beeper/ai-bridge/pkg/textfs"
)

const memorySnippetMaxChars = 700

var keywordTokenRE = regexp.MustCompile(`[A-Za-z0-9_]+`)

const memoryStatusTimeout = 3 * time.Second
const memorySearchTimeout = 10 * time.Second
const memoryManagerInitTimeout = 10 * time.Second

type MemorySearchManager struct {
	runtime      Runtime
	db           *dbutil.Database
	bridgeID     string
	loginID      string
	agentID      string
	cfg          *memorycore.ResolvedConfig
	status       memorycore.ProviderStatus
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
	Fallback          *memorycore.FallbackStatus
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

func GetMemorySearchManager(runtime Runtime, agentID string) (*MemorySearchManager, string) {
	if runtime == nil {
		return nil, "memory search unavailable"
	}
	db := runtime.BridgeDB()
	if db == nil {
		return nil, "memory search unavailable"
	}
	cfg, err := runtime.ResolveConfig(agentID)
	if err != nil || cfg == nil {
		if err != nil {
			return nil, err.Error()
		}
		return nil, "memory search disabled"
	}

	bridgeID := runtime.BridgeID()
	loginID := runtime.LoginID()
	if agentID == "" {
		agentID = "default"
	}

	cacheKey := memoryManagerCacheKey(bridgeID, loginID, agentID, cfg)

	memoryManagerCache.mu.Lock()
	defer memoryManagerCache.mu.Unlock()
	if existing := memoryManagerCache.managers[cacheKey]; existing != nil {
		return existing, ""
	}

	manager := &MemorySearchManager{
		runtime:  runtime,
		db:       db,
		bridgeID: bridgeID,
		loginID:  loginID,
		agentID:  agentID,
		cfg:      cfg,
		status: memorycore.ProviderStatus{
			Provider: "builtin",
			Model:    "lexical",
		},
		log: runtime.Logger().With().Str("component", "memory").Logger(),
	}
	if hasSource(cfg.Sources, "memory") || hasSource(cfg.Sources, "workspace") {
		manager.dirty = true
	}

	initCtx, initCancel := context.WithTimeout(context.Background(), memoryManagerInitTimeout)
	defer initCancel()
	manager.ensureSchema(initCtx)
	manager.ensureDefaultMemoryFiles(initCtx)
	manager.ensureIntervalSync()
	memoryManagerCache.managers[cacheKey] = manager
	return manager, ""
}

func (m *MemorySearchManager) Status() memorycore.ProviderStatus {
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
	_ = ctx
	return false
}

func (m *MemorySearchManager) ProbeEmbeddingAvailability(ctx context.Context) (bool, string) {
	_ = ctx
	return false, "embeddings disabled (lexical memory mode)"
}

func (m *MemorySearchManager) StatusDetails(ctx context.Context) (*MemorySearchStatus, error) {
	if m == nil {
		return nil, errors.New("memory search unavailable")
	}
	// Avoid hanging on SQLite busy/locks during indexing.
	statusCtx, cancel := context.WithTimeout(ctx, memoryStatusTimeout)
	defer cancel()
	start := time.Now()
	m.log.Info().Dur("timeout", memoryStatusTimeout).Msg("memory status: start")

	// Snapshot mutable fields under mu to avoid data races with sync().
	m.mu.Lock()
	dirty := m.dirty
	indexGen := m.indexGen
	m.mu.Unlock()

	workspaceDir := ""
	if m.runtime != nil {
		workspaceDir = m.runtime.ResolvePromptWorkspaceDir()
	}
	status := &MemorySearchStatus{
		Dirty:             dirty,
		WorkspaceDir:      workspaceDir,
		DBPath:            resolveMemoryDBPath(m.cfg, m.agentID),
		Provider:          m.status.Provider,
		Model:             m.status.Model,
		RequestedProvider: m.cfg.Provider,
		Sources:           slices.Clone(m.cfg.Sources),
		ExtraPaths:        resolveStatusExtraPaths(m.cfg.ExtraPaths, workspaceDir),
		Fallback:          m.status.Fallback,
	}

	genSQL, genArgs := generationFilterSQL(5, indexGen)
	sourceSQL, sourceArgs := sourceFilterSQL(4, m.cfg.Sources)
	chunkArgs := []any{m.bridgeID, m.loginID, m.agentID}
	chunkArgs = append(chunkArgs, sourceArgs...)
	chunkArgs = append(chunkArgs, genArgs...)
	row := m.db.QueryRow(statusCtx,
		`SELECT COUNT(*) FROM ai_memory_chunks
         WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3`+sourceSQL+genSQL,
		chunkArgs...,
	)
	_ = row.Scan(&status.Chunks)

	files := 0
	if hasSource(m.cfg.Sources, "memory") {
		row = m.db.QueryRow(statusCtx,
			`SELECT COUNT(*) FROM ai_memory_files WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3 AND source=$4`,
			m.bridgeID, m.loginID, m.agentID, "memory",
		)
		var count int
		_ = row.Scan(&count)
		files += count
	}
	if hasSource(m.cfg.Sources, "workspace") {
		row = m.db.QueryRow(statusCtx,
			`SELECT COUNT(*) FROM ai_memory_files WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3 AND source=$4`,
			m.bridgeID, m.loginID, m.agentID, "workspace",
		)
		var count int
		_ = row.Scan(&count)
		files += count
	}
	if hasSource(m.cfg.Sources, "sessions") {
		row = m.db.QueryRow(statusCtx,
			`SELECT COUNT(*) FROM ai_memory_session_files WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3`,
			m.bridgeID, m.loginID, m.agentID,
		)
		var count int
		_ = row.Scan(&count)
		files += count
	}
	status.Files = files

	status.SourceCounts = buildSourceCounts(statusCtx, m, indexGen)

	cacheStatus := &MemorySearchCacheStatus{Enabled: m.cfg.Cache.Enabled, MaxEntries: m.cfg.Cache.MaxEntries}
	if m.cfg.Cache.Enabled {
		row := m.db.QueryRow(statusCtx,
			`SELECT COUNT(*) FROM ai_memory_embedding_cache
             WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3`,
			m.bridgeID, m.loginID, m.agentID,
		)
		_ = row.Scan(&cacheStatus.Entries)
	}
	status.Cache = cacheStatus

	status.FTS = &MemorySearchFTSStatus{
		Enabled:   true,
		Available: m.ftsAvailable,
		Error:     m.ftsError,
	}
	status.Vector = &MemorySearchVectorStatus{
		Enabled:   false,
		Available: nil,
	}

	status.Batch = &MemorySearchBatchStatus{
		Enabled: false,
	}

	m.log.Info().
		Dur("dur", time.Since(start)).
		Int("files", status.Files).
		Int("chunks", status.Chunks).
		Msg("memory status: done")
	return status, nil
}

func buildSourceCounts(ctx context.Context, m *MemorySearchManager, indexGen string) []MemorySearchSourceCount {
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
		genSQL, genArgs := generationFilterSQL(5, indexGen)
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

func (m *MemorySearchManager) Search(ctx context.Context, query string, opts memorycore.SearchOptions) ([]memorycore.SearchResult, error) {
	if m == nil {
		return nil, errors.New("memory search unavailable")
	}

	// Snapshot indexGen under mu to avoid data races with sync().
	// TryLock: if sync() holds mu we read a potentially stale value, which is
	// acceptable — the generation filter only affects which chunks are returned.
	var indexGen string
	var shouldSync bool
	if m.mu.TryLock() {
		indexGen = m.indexGen
		shouldSync = m.cfg.Sync.OnSearch && (m.dirty || m.sessionsDirty)
		m.mu.Unlock()
	}

	m.warmSession(ctx, opts.SessionKey)
	if shouldSync {
		go func(sessionKey string) {
			if err := m.sync(context.Background(), sessionKey, false); err != nil {
				m.log.Warn().Msg("memory sync failed (search): " + err.Error())
			}
		}(opts.SessionKey)
	}

	mode := strings.ToLower(strings.TrimSpace(opts.Mode))
	switch mode {
	case "", "auto", "semantic", "keyword", "hybrid", "list":
	default:
		mode = "auto"
	}

	cleaned := strings.TrimSpace(query)
	if mode != "list" && cleaned == "" {
		return []memorycore.SearchResult{}, nil
	}

	maxResults := m.cfg.Query.MaxResults
	if opts.MaxResults > 0 {
		maxResults = opts.MaxResults
	}
	if maxResults <= 0 {
		maxResults = memorycore.DefaultMaxResults
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

	chunkResults := []memorycore.HybridKeywordResult{}
	if m.ftsAvailable {
		results, err := m.searchKeyword(ctx, cleaned, candidates, sources, pathPrefix, indexGen)
		if err == nil {
			chunkResults = results
		}
	}
	if len(chunkResults) == 0 && (mode == "semantic" || mode == "hybrid") {
		results, err := m.searchKeywordScan(ctx, cleaned, candidates, sources, pathPrefix, indexGen)
		if err == nil {
			chunkResults = results
		}
	}
	if len(chunkResults) == 0 {
		results, err := m.searchKeywordFiles(ctx, cleaned, candidates, sources, pathPrefix)
		if err != nil {
			return nil, err
		}
		return clampInjectedChars(filterAndLimit(results, minScore, maxResults), m.cfg.Query.MaxInjectedChars), nil
	}
	return clampInjectedChars(filterAndLimit(keywordResultsToSearch(chunkResults), minScore, maxResults), m.cfg.Query.MaxInjectedChars), nil
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

func (m *MemorySearchManager) listRecentFiles(ctx context.Context, sources []string, pathPrefix string, limit int) ([]memorycore.SearchResult, error) {
	if m == nil || m.db == nil {
		return nil, errors.New("memory search unavailable")
	}
	if limit <= 0 {
		limit = memorycore.DefaultMaxResults
	}
	if limit > 200 {
		limit = 200
	}

	baseArgs := []any{m.bridgeID, m.loginID, m.agentID}
	sourceSQL, sourceArgs := sourceFilterSQL(4, sources)
	pathSQL, pathArgs := pathPrefixFilterSQL(4+len(sourceArgs), pathPrefix)
	// Overfetch and filter client-side (extension allowlist, size cap).
	overfetch := limit * 5
	if overfetch < 50 {
		overfetch = 50
	}
	if overfetch > 500 {
		overfetch = 500
	}

	args := append(baseArgs, sourceArgs...)
	args = append(args, pathArgs...)
	args = append(args, overfetch)

	rows, err := m.db.Query(ctx,
		`SELECT path, source, substr(content, 1, 8192), length(content)
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

	results := make([]memorycore.SearchResult, 0, limit)
	for rows.Next() {
		var path, source, content string
		var length int
		if err := rows.Scan(&path, &source, &content, &length); err != nil {
			return nil, err
		}
		if ok, _, _ := textfs.IsAllowedTextNotePath(path); !ok {
			continue
		}
		if length > textfs.NoteMaxBytesDefault() {
			continue
		}
		results = append(results, memorycore.SearchResult{
			Path:      path,
			StartLine: 1,
			EndLine:   1,
			Score:     1,
			Snippet:   truncateSnippet(normalizeNewlines(content)),
			Source:    source,
		})
		if len(results) >= limit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

func (m *MemorySearchManager) searchKeywordScan(ctx context.Context, query string, limit int, sources []string, pathPrefix string, indexGen string) ([]memorycore.HybridKeywordResult, error) {
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
	genSQL, genArgs := generationFilterSQL(5+len(sourceArgs), indexGen)
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
		r     memorycore.HybridKeywordResult
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
			r: memorycore.HybridKeywordResult{
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
	out := make([]memorycore.HybridKeywordResult, 0, len(scoredResults))
	for _, entry := range scoredResults {
		out = append(out, entry.r)
	}
	return out, nil
}

func (m *MemorySearchManager) searchKeywordFiles(ctx context.Context, query string, limit int, sources []string, pathPrefix string) ([]memorycore.SearchResult, error) {
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

	// Overfetch so we can filter by allowlist + size cap without running multiple queries.
	overfetch := limit * 10
	if overfetch < 50 {
		overfetch = 50
	}
	if overfetch > 500 {
		overfetch = 500
	}

	baseArgs := []any{m.bridgeID, m.loginID, m.agentID}
	sourceSQL, sourceArgs := sourceFilterSQL(4, sources)
	pathSQL, pathArgs := pathPrefixFilterSQL(4+len(sourceArgs), pathPrefix)
	args := append(baseArgs, sourceArgs...)
	args = append(args, pathArgs...)

	whereParts := make([]string, 0, len(tokens))
	for i, token := range tokens {
		whereParts = append(whereParts, fmt.Sprintf(" AND LOWER(content) LIKE $%d", 4+len(sourceArgs)+len(pathArgs)+i))
		args = append(args, "%"+token+"%")
	}
	args = append(args, overfetch)

	rows, err := m.db.Query(ctx,
		`SELECT path, source, substr(content, 1, 8192), length(content)
         FROM ai_memory_files
         WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3`+sourceSQL+pathSQL+strings.Join(whereParts, "")+`
         ORDER BY updated_at DESC
         LIMIT $`+fmt.Sprintf("%d", len(args)),
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := make([]memorycore.SearchResult, 0, limit)
	for rows.Next() {
		var path, source, content string
		var length int
		if err := rows.Scan(&path, &source, &content, &length); err != nil {
			return nil, err
		}
		if ok, _, _ := textfs.IsAllowedTextNotePath(path); !ok {
			continue
		}
		if length > textfs.NoteMaxBytesDefault() {
			continue
		}
		lower := strings.ToLower(content)
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
		results = append(results, memorycore.SearchResult{
			Path:      path,
			StartLine: 1,
			EndLine:   1,
			Score:     score,
			Snippet:   truncateSnippet(normalizeNewlines(content)),
			Source:    source,
		})
		if len(results) >= limit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

func (m *MemorySearchManager) ReadFile(ctx context.Context, relPath string, from, lines *int) (map[string]any, error) {
	if m == nil {
		return nil, errors.New("memory search unavailable")
	}
	path, err := textfs.NormalizePath(relPath)
	if err != nil {
		return nil, errors.New("path required")
	}
	if ok, ext, reason := textfs.IsAllowedTextNotePath(path); !ok {
		switch reason {
		case "missing_extension":
			return nil, errors.New("path must include a file extension (allowed: " + strings.Join(textfs.AllowedNoteExtensions(), ", ") + ")")
		case "unsupported_extension":
			if ext == "" {
				ext = "(unknown)"
			}
			return nil, errors.New("unsupported file type " + ext + " (allowed: " + strings.Join(textfs.AllowedNoteExtensions(), ", ") + ")")
		default:
			return nil, errors.New("path required")
		}
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
	if len(entry.Content) > textfs.NoteMaxBytesDefault() {
		return nil, fmt.Errorf("file too large to read via memory_get (%d > %d bytes)", len(entry.Content), textfs.NoteMaxBytesDefault())
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

func filterAndLimit(results []memorycore.SearchResult, minScore float64, maxResults int) []memorycore.SearchResult {
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
func clampInjectedChars(results []memorycore.SearchResult, maxChars int) []memorycore.SearchResult {
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

func keywordResultsToSearch(results []memorycore.HybridKeywordResult) []memorycore.SearchResult {
	out := make([]memorycore.SearchResult, 0, len(results))
	for _, entry := range results {
		out = append(out, memorycore.SearchResult{
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

func memoryManagerCacheKey(bridgeID, loginID, agentID string, cfg *memorycore.ResolvedConfig) string {
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
	// Memory search indexes allowed text notes across the virtual workspace.
	if ok, _, _ := textfs.IsAllowedTextNotePath(path); ok {
		return true
	}
	if len(extraPaths) == 0 {
		return false
	}
	normalizedExtra := normalizeExtraPaths(extraPaths)
	for _, extra := range normalizedExtra {
		if ok, _, _ := textfs.IsAllowedTextNotePath(extra); ok {
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

func resolveMemoryDBPath(_ *memorycore.ResolvedConfig, _ string) string {
	return "bridge.sqlite (vfs)"
}
