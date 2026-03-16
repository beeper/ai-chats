package memory

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/util/dbutil"

	iruntime "github.com/beeper/agentremote/pkg/integrations/runtime"
	memorycore "github.com/beeper/agentremote/pkg/memory"
	pkgruntime "github.com/beeper/agentremote/pkg/runtime"
	"github.com/beeper/agentremote/pkg/textfs"
)

const memorySnippetMaxChars = 700

func extractKeywordTokens(query string) []string {
	tokens := memorycore.TokenRE.FindAllString(query, -1)
	for i, t := range tokens {
		tokens[i] = strings.ToLower(strings.TrimSpace(t))
	}
	return tokens
}

const (
	memoryStatusTimeout      = 3 * time.Second
	memorySearchTimeout      = 10 * time.Second
	memoryManagerInitTimeout = 10 * time.Second
)

type MemorySearchManager struct {
	host         iruntime.Host
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
	startIntervalSync func() // starts the interval sync goroutine exactly once
	intervalStop      chan struct{}
	closeIntervalStop func() // closes intervalStop channel exactly once
	mu                sync.Mutex
}

// baseArgs returns the common (bridge_id, login_id, agent_id) query parameters,
// optionally followed by any extra arguments.
func (m *MemorySearchManager) baseArgs(extra ...any) []any {
	return append([]any{m.bridgeID, m.loginID, m.agentID}, extra...)
}

type MemorySearchStatus struct {
	Files        int
	Chunks       int
	Dirty        bool
	WorkspaceDir string
	DBPath       string
	Provider     string
	Model        string
	Sources      []string
	ExtraPaths   []string
	SourceCounts []MemorySearchSourceCount
	Cache        *MemorySearchCacheStatus
	FTS          *MemorySearchFTSStatus
	Fallback     *memorycore.FallbackStatus
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

var memoryManagerCache = struct {
	mu       sync.Mutex
	managers map[string]*MemorySearchManager
}{
	managers: make(map[string]*MemorySearchManager),
}

func GetMemorySearchManager(host iruntime.Host, agentID string) (*MemorySearchManager, string) {
	if host == nil {
		return nil, "memory search unavailable"
	}
	rawDB := host.BridgeDB()
	if rawDB == nil {
		return nil, "memory search unavailable"
	}
	db, _ := rawDB.(*dbutil.Database)
	if db == nil {
		return nil, "memory search unavailable"
	}
	cfg, err := resolveMemorySearchConfigFromMaps(host.ModuleConfig(moduleName), host.AgentModuleConfig(agentID, moduleName))
	if err != nil {
		return nil, err.Error()
	}
	if cfg == nil {
		return nil, "memory search disabled"
	}

	bridgeID := host.BridgeID()
	loginID := host.LoginID()
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
		host:     host,
		db:       db,
		bridgeID: bridgeID,
		loginID:  loginID,
		agentID:  agentID,
		cfg:      cfg,
		status: memorycore.ProviderStatus{
			Provider: "builtin",
			Model:    "lexical",
		},
		log: iruntime.ZerologFromHost(host).With().Str("component", "memory").Logger(),
	}
	manager.startIntervalSync = sync.OnceFunc(func() {
		interval := time.Duration(manager.cfg.Sync.IntervalMinutes) * time.Minute
		manager.mu.Lock()
		if manager.intervalStop == nil {
			manager.intervalStop = make(chan struct{})
		}
		stopCh := manager.intervalStop
		manager.mu.Unlock()
		manager.log.Debug().Dur("interval", interval).Msg("Memory sync starting interval sync goroutine")
		go func() {
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					if err := manager.sync(context.Background(), "", false); err != nil {
						manager.log.Warn().Msg("memory sync failed (interval): " + err.Error())
					} else {
						manager.log.Debug().Msg("Memory sync interval complete")
					}
				case <-stopCh:
					return
				}
			}
		}()
	})
	manager.closeIntervalStop = sync.OnceFunc(func() {
		manager.mu.Lock()
		stopCh := manager.intervalStop
		manager.mu.Unlock()
		if stopCh != nil {
			close(stopCh)
		}
	})
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

func (m *MemorySearchManager) StatusDetails(ctx context.Context) (*MemorySearchStatus, error) {
	if m == nil {
		return nil, errors.New("memory search unavailable")
	}
	statusCtx, cancel := context.WithTimeout(ctx, memoryStatusTimeout)
	defer cancel()
	start := time.Now()

	m.mu.Lock()
	dirty := m.dirty
	indexGen := m.indexGen
	m.mu.Unlock()

	workspaceDir := ""
	if m.host != nil {
		workspaceDir = m.host.ResolveWorkspaceDir()
	}
	status := &MemorySearchStatus{
		Dirty:        dirty,
		WorkspaceDir: workspaceDir,
		DBPath:       memoryDBPath,
		Provider:     m.status.Provider,
		Model:        m.status.Model,
		Sources:      slices.Clone(m.cfg.Sources),
		ExtraPaths:   resolveStatusExtraPaths(m.cfg.ExtraPaths, workspaceDir),
		Fallback:     m.status.Fallback,
	}

	genSQL, genArgs := generationFilterSQL(5, indexGen)
	sourceSQL, sourceArgs := sourceFilterSQL(4, m.cfg.Sources)
	chunkArgs := m.baseArgs(sourceArgs...)
	chunkArgs = append(chunkArgs, genArgs...)
	row := m.db.QueryRow(statusCtx,
		`SELECT COUNT(*) FROM aichats_memory_chunks
         WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3`+sourceSQL+genSQL,
		chunkArgs...,
	)
	_ = row.Scan(&status.Chunks)

	status.SourceCounts = buildSourceCounts(statusCtx, m, indexGen)
	files := 0
	for _, sc := range status.SourceCounts {
		files += sc.Files
	}
	status.Files = files

	cacheStatus := &MemorySearchCacheStatus{Enabled: m.cfg.Cache.Enabled, MaxEntries: m.cfg.Cache.MaxEntries}
	if m.cfg.Cache.Enabled {
		row := m.db.QueryRow(statusCtx,
			`SELECT COUNT(*) FROM aichats_memory_embedding_cache
             WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3`,
			m.baseArgs()...,
		)
		_ = row.Scan(&cacheStatus.Entries)
	}
	status.Cache = cacheStatus

	status.FTS = &MemorySearchFTSStatus{
		Enabled:   true,
		Available: m.ftsAvailable,
		Error:     m.ftsError,
	}

	m.log.Debug().
		Dur("dur", time.Since(start)).
		Int("files", status.Files).
		Int("chunks", status.Chunks).
		Msg("memory status")
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
		case "memory", "workspace":
			_ = m.db.QueryRow(ctx,
				`SELECT COUNT(*) FROM aichats_memory_files WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3 AND source=$4`,
				m.baseArgs(source)...,
			).Scan(&count.Files)
		case "sessions":
			_ = m.db.QueryRow(ctx,
				`SELECT COUNT(*) FROM aichats_memory_session_files WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3`,
				m.baseArgs()...,
			).Scan(&count.Files)
		}
		genSQL, genArgs := generationFilterSQL(5, indexGen)
		args := m.baseArgs(source)
		args = append(args, genArgs...)
		_ = m.db.QueryRow(ctx,
			`SELECT COUNT(*) FROM aichats_memory_chunks WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3 AND source=$4`+genSQL,
			args...,
		).Scan(&count.Chunks)
		out = append(out, count)
	}
	return out
}

func (m *MemorySearchManager) Search(ctx context.Context, query string, opts memorycore.SearchOptions) ([]memorycore.SearchResult, error) {
	if m == nil {
		return nil, errors.New("memory search unavailable")
	}

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
		key := strings.ToLower(strings.TrimSpace(raw))
		switch key {
		case "memory", "workspace", "sessions":
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

	queryArgs := m.baseArgs()
	sourceSQL, sourceArgs := sourceFilterSQL(4, sources)
	pathSQL, pathArgs := pathPrefixFilterSQL(4+len(sourceArgs), pathPrefix)
	overfetch := clampOverfetch(limit, 5)

	args := append(queryArgs, sourceArgs...)
	args = append(args, pathArgs...)
	args = append(args, overfetch)

	rows, err := m.db.Query(ctx,
		`SELECT path, source, substr(content, 1, 8192), length(content)
         FROM aichats_memory_files
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
	tokens := extractKeywordTokens(query)
	if len(tokens) == 0 {
		return nil, nil
	}

	scanLimit := max(200, min(1000, limit*10))

	scanArgs := m.baseArgs(m.status.Model)
	sourceSQL, sourceArgs := sourceFilterSQL(5, sources)
	genSQL, genArgs := generationFilterSQL(5+len(sourceArgs), indexGen)
	pathSQL, pathArgs := pathPrefixFilterSQL(5+len(sourceArgs)+len(genArgs), pathPrefix)
	args := append(scanArgs, sourceArgs...)
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
         FROM aichats_memory_chunks
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
		return cmp.Compare(b.score, a.score)
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
	tokens := extractKeywordTokens(query)
	if len(tokens) == 0 {
		return nil, nil
	}

	overfetch := clampOverfetch(limit, 10)

	fileArgs := m.baseArgs()
	sourceSQL, sourceArgs := sourceFilterSQL(4, sources)
	pathSQL, pathArgs := pathPrefixFilterSQL(4+len(sourceArgs), pathPrefix)
	args := append(fileArgs, sourceArgs...)
	args = append(args, pathArgs...)

	whereParts := make([]string, 0, len(tokens))
	for i, token := range tokens {
		whereParts = append(whereParts, fmt.Sprintf(" AND LOWER(content) LIKE $%d", 4+len(sourceArgs)+len(pathArgs)+i))
		args = append(args, "%"+token+"%")
	}
	args = append(args, overfetch)

	rows, err := m.db.Query(ctx,
		`SELECT path, source, substr(content, 1, 8192), length(content)
         FROM aichats_memory_files
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
		"sources":      sources,
		"extraPaths":   extra,
		"store":        map[string]any{"driver": cfg.Store.Driver, "path": cfg.Store.Path},
		"chunking":     cfg.Chunking,
		"sync":         cfg.Sync,
		"query":        cfg.Query,
		"cache":        cfg.Cache,
		"experimental": cfg.Experimental,
	}
	raw, _ := json.Marshal(payload)
	return fmt.Sprintf("%s:%s:%s:%s", bridgeID, loginID, agentID, memorycore.HashText(string(raw)))
}

func clampOverfetch(limit, multiplier int) int {
	return max(50, min(500, limit*multiplier))
}

func normalizeNewlines(text string) string {
	return pkgruntime.NormalizeInboundTextNewlines(text)
}

// truncateSnippet truncates text to memorySnippetMaxChars, counting supplementary
// plane characters (> U+FFFF) as 2 units to match UTF-16 encoding width.
func truncateSnippet(text string) string {
	if text == "" {
		return ""
	}
	limit := memorySnippetMaxChars
	count := 0
	for i, r := range text {
		inc := 1
		if r > 0xFFFF {
			inc = 2
		}
		if count+inc > limit {
			return text[:i]
		}
		count += inc
	}
	return text
}

func isAllowedMemoryPath(path string, extraPaths []string) bool {
	if ok, _, _ := textfs.IsAllowedTextNotePath(path); ok {
		return true
	}
	return isExtraPath(path, normalizeExtraPaths(extraPaths))
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

const memoryDBPath = "bridge.sqlite (vfs)"
