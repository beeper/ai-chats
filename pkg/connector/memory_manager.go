package connector

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/util/dbutil"

	"github.com/beeper/ai-bridge/pkg/memory"
	"github.com/beeper/ai-bridge/pkg/textfs"
)

const memorySnippetMaxChars = 700

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
	sessionWarm       map[string]struct{}
	watchTimer        *time.Timer
	sessionWatchTimer *time.Timer
	sessionWatchKey   string
	intervalOnce      sync.Once
	vectorConn        *sql.Conn
	vectorReady       bool
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
	if hasSource(cfg.Sources, "memory") {
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
	m.ensureVectorConn(ctx)
	m.mu.Lock()
	ready := m.vectorReady
	m.mu.Unlock()
	return ready
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
		return nil, fmt.Errorf("memory search unavailable")
	}
	workspaceDir := resolvePromptWorkspaceDir()
	status := &MemorySearchStatus{
		Dirty:             m.dirty,
		WorkspaceDir:      workspaceDir,
		DBPath:            resolveMemoryDBPath(m.cfg, m.agentID),
		Provider:          m.status.Provider,
		Model:             m.status.Model,
		RequestedProvider: m.cfg.Provider,
		Sources:           append([]string{}, m.cfg.Sources...),
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

	vectorAvailable := (*bool)(nil)
	if m.vectorReady {
		ready := true
		vectorAvailable = &ready
	} else if m.vectorError != "" {
		ready := false
		vectorAvailable = &ready
	}
	status.Vector = &MemorySearchVectorStatus{
		Enabled:       m.cfg.Store.Vector.Enabled,
		Available:     vectorAvailable,
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
		return nil, fmt.Errorf("memory search unavailable")
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

	cleaned := strings.TrimSpace(query)
	if cleaned == "" {
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

	keywordResults := []memory.HybridKeywordResult{}
	if m.cfg.Query.Hybrid.Enabled && m.ftsAvailable {
		results, err := m.searchKeyword(ctx, cleaned, candidates)
		if err == nil {
			keywordResults = results
		}
	}

	vectorResults := []memory.HybridVectorResult{}
	if m.cfg.Store.Vector.Enabled {
		queryVec, err := m.embedQueryWithTimeout(ctx, cleaned)
		if err != nil {
			return nil, err
		}
		hasVector := false
		for _, v := range queryVec {
			if v != 0 {
				hasVector = true
				break
			}
		}
		if hasVector {
			results, err := m.searchVector(ctx, queryVec, candidates)
			if err == nil {
				vectorResults = results
			}
		}
	}

	if !m.cfg.Query.Hybrid.Enabled {
		return filterAndLimit(vectorResultsToSearch(vectorResults), minScore, maxResults), nil
	}

	merged := memory.MergeHybridResults(vectorResults, keywordResults, m.cfg.Query.Hybrid.VectorWeight, m.cfg.Query.Hybrid.TextWeight)
	return filterAndLimit(merged, minScore, maxResults), nil
}

func (m *MemorySearchManager) ReadFile(ctx context.Context, relPath string, from, lines *int) (map[string]any, error) {
	if m == nil {
		return nil, fmt.Errorf("memory search unavailable")
	}
	path, err := textfs.NormalizePath(relPath)
	if err != nil {
		return nil, fmt.Errorf("path required")
	}
	if !strings.HasSuffix(strings.ToLower(path), ".md") {
		return nil, fmt.Errorf("path required")
	}
	if !isAllowedMemoryPath(path, m.cfg.ExtraPaths) {
		return nil, fmt.Errorf("path required")
	}

	store := textfs.NewStore(m.db, m.bridgeID, m.loginID, m.agentID)
	entry, found, err := store.Read(ctx, path)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("file not found")
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

func memoryManagerCacheKey(bridgeID, loginID, agentID string, cfg *memory.ResolvedConfig) string {
	if cfg == nil {
		return fmt.Sprintf("%s:%s:%s", bridgeID, loginID, agentID)
	}
	sources := append([]string{}, cfg.Sources...)
	extra := append([]string{}, cfg.ExtraPaths...)
	sort.Strings(sources)
	sort.Strings(extra)
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
	sort.Strings(keys)
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
	if textfs.IsMemoryPath(path) {
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
	sort.Strings(out)
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
	sort.Strings(out)
	return out
}

func resolveMemoryDBPath(_ *memory.ResolvedConfig, _ string) string {
	return "bridge.sqlite (vfs)"
}
