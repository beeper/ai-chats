package memory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	memorycore "github.com/beeper/ai-bridge/pkg/memory"
	"github.com/beeper/ai-bridge/pkg/textfs"
)

const lexicalProviderKey = "builtin-lexical-v1"

func (m *MemorySearchManager) ensureSchema(ctx context.Context) {
	if m == nil || m.db == nil {
		return
	}
	_, err := m.db.Exec(ctx,
		`CREATE VIRTUAL TABLE IF NOT EXISTS ai_memory_chunks_fts USING fts5(
			text,
			id UNINDEXED,
			path UNINDEXED,
			source UNINDEXED,
			model UNINDEXED,
			start_line UNINDEXED,
			end_line UNINDEXED,
			bridge_id UNINDEXED,
			login_id UNINDEXED,
			agent_id UNINDEXED
		);`,
	)
	if err != nil {
		m.ftsAvailable = false
		m.ftsError = err.Error()
		return
	}
	m.ftsAvailable = true
	m.ftsError = ""
}

// syncWithProgress is like sync but calls onProgress during indexing.
func (m *MemorySearchManager) syncWithProgress(ctx context.Context, sessionKey string, force bool, onProgress func(completed, total int, label string)) error {
	if m == nil {
		return errors.New("memory search unavailable")
	}
	m.syncProgress = onProgress
	err := m.sync(ctx, sessionKey, force)
	m.syncProgress = nil
	return err
}

func (m *MemorySearchManager) sync(ctx context.Context, sessionKey string, force bool) error {
	if m == nil {
		return errors.New("memory search unavailable")
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	needsFullReindex, err := m.needsFullReindex(ctx, force)
	if err != nil {
		return err
	}
	generation := strings.TrimSpace(m.indexGen)
	if needsFullReindex || generation == "" {
		generation = uuid.NewString()
	}
	if needsFullReindex {
		return m.syncFullReindex(ctx, sessionKey, generation)
	}
	return m.syncIncremental(ctx, sessionKey, generation)
}

// syncFullReindex rebuilds chunks for all configured sources and removes old generations.
func (m *MemorySearchManager) syncFullReindex(ctx context.Context, sessionKey, generation string) error {
	if err := m.indexMemoryFiles(ctx, true, generation); err != nil {
		return err
	}
	if m.cfg.Experimental.SessionMemory && hasSource(m.cfg.Sources, "sessions") {
		if err := m.syncSessions(ctx, true, sessionKey, generation); err != nil {
			return err
		}
		m.sessionsDirty = false
	}
	if err := m.updateMeta(ctx, generation); err != nil {
		return err
	}
	m.deleteOldGenerations(ctx, generation)
	m.indexGen = generation
	if hasSource(m.cfg.Sources, "memory") || hasSource(m.cfg.Sources, "workspace") {
		m.dirty = false
	}
	return nil
}

// syncIncremental indexes changed files only.
func (m *MemorySearchManager) syncIncremental(ctx context.Context, sessionKey, generation string) error {
	if err := m.indexMemoryFiles(ctx, false, generation); err != nil {
		return err
	}
	if hasSource(m.cfg.Sources, "memory") || hasSource(m.cfg.Sources, "workspace") {
		m.dirty = false
	}
	if m.cfg.Experimental.SessionMemory && hasSource(m.cfg.Sources, "sessions") {
		if err := m.syncSessions(ctx, false, sessionKey, generation); err != nil {
			return err
		}
		m.sessionsDirty = false
	}
	if err := m.updateMeta(ctx, generation); err != nil {
		return err
	}
	m.indexGen = generation
	return nil
}

func (m *MemorySearchManager) needsFullReindex(ctx context.Context, force bool) (bool, error) {
	if force {
		return true, nil
	}
	var provider, model, providerKey, indexGen string
	var chunkTokens, chunkOverlap int
	row := m.db.QueryRow(ctx,
		`SELECT provider, model, provider_key, chunk_tokens, chunk_overlap, index_generation
         FROM ai_memory_meta
         WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3`,
		m.bridgeID, m.loginID, m.agentID,
	)
	switch err := row.Scan(&provider, &model, &providerKey, &chunkTokens, &chunkOverlap, &indexGen); err {
	case nil:
		if provider != m.status.Provider ||
			model != m.status.Model ||
			providerKey != lexicalProviderKey ||
			chunkTokens != m.cfg.Chunking.Tokens ||
			chunkOverlap != m.cfg.Chunking.Overlap {
			return true, nil
		}
		m.indexGen = strings.TrimSpace(indexGen)
		if m.indexGen == "" {
			m.indexGen = m.deriveIndexGeneration(ctx)
			if m.indexGen == "" {
				return true, nil
			}
		}
		return false, nil
	case sql.ErrNoRows:
		return true, nil
	default:
		return false, err
	}
}

func (m *MemorySearchManager) updateMeta(ctx context.Context, generation string) error {
	_, err := m.db.Exec(ctx,
		`INSERT INTO ai_memory_meta
           (bridge_id, login_id, agent_id, provider, model, provider_key, chunk_tokens, chunk_overlap, vector_dims, index_generation, updated_at)
         VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NULL, $9, $10)
         ON CONFLICT (bridge_id, login_id, agent_id)
         DO UPDATE SET provider=excluded.provider, model=excluded.model, provider_key=excluded.provider_key,
           chunk_tokens=excluded.chunk_tokens, chunk_overlap=excluded.chunk_overlap,
           vector_dims=NULL, index_generation=excluded.index_generation, updated_at=excluded.updated_at`,
		m.bridgeID, m.loginID, m.agentID,
		m.status.Provider, m.status.Model, lexicalProviderKey,
		m.cfg.Chunking.Tokens, m.cfg.Chunking.Overlap,
		generation, time.Now().UnixMilli(),
	)
	return err
}

func (m *MemorySearchManager) deriveIndexGeneration(ctx context.Context) string {
	if m == nil {
		return ""
	}
	row := m.db.QueryRow(ctx,
		`SELECT id FROM ai_memory_chunks
         WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3
         ORDER BY updated_at DESC
         LIMIT 1`,
		m.bridgeID, m.loginID, m.agentID,
	)
	var id string
	if err := row.Scan(&id); err != nil {
		return ""
	}
	if id == "" {
		return ""
	}
	if parts := strings.SplitN(id, ":", 2); len(parts) == 2 {
		return parts[0]
	}
	return ""
}

func (m *MemorySearchManager) indexMemoryFiles(ctx context.Context, force bool, generation string) error {
	prepared, activeBySource, err := m.prepareMemoryFiles(ctx, force, generation)
	if err != nil {
		return err
	}

	for _, pc := range prepared {
		if err := m.writeContent(ctx, pc); err != nil {
			return err
		}
	}

	for _, source := range []string{"memory", "workspace"} {
		active := activeBySource[source]
		if active == nil {
			continue
		}
		if err := m.removeStaleChunksForSource(ctx, active, generation, source); err != nil {
			return err
		}
	}
	return nil
}

// prepareMemoryFiles reads memory/workspace files and chunks changed entries.
func (m *MemorySearchManager) prepareMemoryFiles(ctx context.Context, force bool, generation string) ([]*preparedContent, map[string]map[string]textfs.FileEntry, error) {
	store := textfs.NewStore(m.db, m.bridgeID, m.loginID, m.agentID)
	entries, err := store.List(ctx)
	if err != nil {
		return nil, nil, err
	}

	extraPaths := normalizeExtraPaths(m.cfg.ExtraPaths)
	activeBySource := make(map[string]map[string]textfs.FileEntry)
	for _, entry := range entries {
		path := strings.TrimSpace(entry.Path)
		if path == "" {
			continue
		}
		if ok, _, _ := textfs.IsAllowedTextNotePath(path); !ok {
			continue
		}
		if len(entry.Content) > textfs.NoteMaxBytesDefault() {
			continue
		}
		source := strings.ToLower(strings.TrimSpace(entry.Source))
		if source == "" {
			source = textfs.ClassifySource(path)
		}
		switch source {
		case "memory":
			if !hasSource(m.cfg.Sources, "memory") && !isExtraPath(path, extraPaths) {
				continue
			}
		case "workspace":
			if !hasSource(m.cfg.Sources, "workspace") && !isExtraPath(path, extraPaths) {
				continue
			}
		default:
			continue
		}
		if activeBySource[source] == nil {
			activeBySource[source] = make(map[string]textfs.FileEntry)
		}
		activeBySource[source][path] = entry
	}

	for _, source := range []string{"memory", "workspace"} {
		if hasSource(m.cfg.Sources, source) && activeBySource[source] == nil {
			activeBySource[source] = make(map[string]textfs.FileEntry)
		}
	}

	total := 0
	for _, group := range activeBySource {
		total += len(group)
	}
	if m.syncProgress != nil {
		m.syncProgress(0, total, "prepare")
	}

	prepared := make([]*preparedContent, 0, total)
	completed := 0
	for _, source := range []string{"memory", "workspace"} {
		active := activeBySource[source]
		if active == nil {
			continue
		}
		for _, entry := range active {
			needs, err := m.needsFileIndex(ctx, entry, source, generation)
			if err != nil {
				return nil, nil, err
			}
			if !force && !needs {
				completed++
				continue
			}
			if m.syncProgress != nil {
				m.syncProgress(completed, total, entry.Path)
			}
			pc, err := m.prepareContent(ctx, entry.Path, source, entry.Content, generation)
			if err != nil {
				return nil, nil, err
			}
			if pc != nil {
				prepared = append(prepared, pc)
			}
			completed++
		}
	}
	if m.syncProgress != nil {
		m.syncProgress(total, total, "cleanup")
	}
	return prepared, activeBySource, nil
}

func (m *MemorySearchManager) needsFileIndex(ctx context.Context, entry textfs.FileEntry, source, generation string) (bool, error) {
	var updatedAt sql.NullInt64
	genSQL, genArgs := generationFilterSQL(7, generation)
	args := []any{m.bridgeID, m.loginID, m.agentID, entry.Path, source, m.status.Model}
	args = append(args, genArgs...)
	row := m.db.QueryRow(ctx,
		`SELECT MAX(updated_at) FROM ai_memory_chunks
         WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3 AND path=$4 AND source=$5 AND model=$6`+genSQL,
		args...,
	)
	if err := row.Scan(&updatedAt); err != nil {
		return true, err
	}
	if !updatedAt.Valid {
		return true, nil
	}
	if entry.UpdatedAt > updatedAt.Int64 {
		return true, nil
	}
	return false, nil
}

type preparedContent struct {
	Path       string
	Source     string
	Generation string
	Chunks     []memorycore.Chunk
}

func (m *MemorySearchManager) prepareContent(_ context.Context, path, source, content, generation string) (*preparedContent, error) {
	cleanContent := normalizeNewlines(content)
	chunks := memorycore.ChunkMarkdown(cleanContent, m.cfg.Chunking.Tokens, m.cfg.Chunking.Overlap)
	filtered := chunks[:0]
	for _, chunk := range chunks {
		if strings.TrimSpace(chunk.Text) == "" {
			continue
		}
		filtered = append(filtered, chunk)
	}
	chunks = filtered
	if len(chunks) == 0 {
		return nil, nil
	}
	return &preparedContent{
		Path:       path,
		Source:     source,
		Generation: generation,
		Chunks:     chunks,
	}, nil
}

func (m *MemorySearchManager) writeContent(ctx context.Context, pc *preparedContent) error {
	if pc == nil || len(pc.Chunks) == 0 {
		return nil
	}
	now := time.Now().UnixMilli()
	newIDs := make([]string, 0, len(pc.Chunks))
	for _, chunk := range pc.Chunks {
		chunkID := buildChunkID(pc.Generation)
		newIDs = append(newIDs, chunkID)
		_, err := m.db.Exec(ctx,
			`INSERT INTO ai_memory_chunks
             (id, bridge_id, login_id, agent_id, path, source, start_line, end_line, hash, model, text, embedding, updated_at)
             VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, '[]', $12)`,
			chunkID, m.bridgeID, m.loginID, m.agentID, pc.Path, pc.Source, chunk.StartLine, chunk.EndLine, chunk.Hash,
			m.status.Model, chunk.Text, now,
		)
		if err != nil {
			return err
		}
		if m.ftsAvailable {
			if _, err := m.db.Exec(ctx,
				`INSERT INTO ai_memory_chunks_fts
                 (text, id, path, source, model, start_line, end_line, bridge_id, login_id, agent_id)
                 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
				chunk.Text, chunkID, pc.Path, pc.Source, m.status.Model, chunk.StartLine, chunk.EndLine,
				m.bridgeID, m.loginID, m.agentID,
			); err != nil {
				m.log.Warn().Err(err).Str("chunk_id", chunkID).Msg("FTS insert failed")
			}
		}
	}
	return m.deletePathChunks(ctx, pc.Path, pc.Source, pc.Generation, newIDs)
}

func (m *MemorySearchManager) indexContent(ctx context.Context, path, source, content, generation string) error {
	pc, err := m.prepareContent(ctx, path, source, content, generation)
	if err != nil {
		return err
	}
	return m.writeContent(ctx, pc)
}

func buildChunkID(generation string) string {
	generation = strings.TrimSpace(generation)
	if generation == "" {
		return uuid.NewString()
	}
	return generation + ":" + uuid.NewString()
}

func (m *MemorySearchManager) deletePathChunks(ctx context.Context, path, source, generation string, keepIDs []string) error {
	if m == nil {
		return nil
	}
	genFilter, genArgs := generationFilterSQL(7, generation)
	args := []any{m.bridgeID, m.loginID, m.agentID, path, source, m.status.Model}
	args = append(args, genArgs...)

	placeholders := ""
	var placeholderArgs []any
	if len(keepIDs) > 0 {
		ids := make([]string, 0, len(keepIDs))
		for _, id := range keepIDs {
			if id != "" {
				ids = append(ids, id)
			}
		}
		if len(ids) > 0 {
			start := 7 + len(genArgs)
			parts := make([]string, 0, len(ids))
			for i, id := range ids {
				parts = append(parts, fmt.Sprintf("$%d", start+i))
				placeholderArgs = append(placeholderArgs, id)
			}
			placeholders = " AND id NOT IN (" + strings.Join(parts, ",") + ")"
		}
	}
	args = append(args, placeholderArgs...)

	rows, err := m.db.Query(ctx,
		`SELECT id FROM ai_memory_chunks
         WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3 AND path=$4 AND source=$5 AND model=$6`+genFilter+placeholders,
		args...,
	)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if len(ids) == 0 {
		return nil
	}
	for _, id := range ids {
		if _, err := m.db.Exec(ctx,
			`DELETE FROM ai_memory_chunks_fts
             WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3 AND id=$4`,
			m.bridgeID, m.loginID, m.agentID, id,
		); err != nil {
			m.log.Warn().Err(err).Str("chunk_id", id).Msg("FTS delete failed")
		}
	}
	_, err = m.db.Exec(ctx,
		`DELETE FROM ai_memory_chunks
         WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3 AND path=$4 AND source=$5 AND model=$6`+genFilter+placeholders,
		args...,
	)
	return err
}

func (m *MemorySearchManager) removeStaleChunksForSource(ctx context.Context, active map[string]textfs.FileEntry, generation string, source string) error {
	source = strings.TrimSpace(source)
	if source == "" {
		return nil
	}
	genSQL, genArgs := generationFilterSQL(5, generation)
	args := []any{m.bridgeID, m.loginID, m.agentID, source}
	args = append(args, genArgs...)
	rows, err := m.db.Query(ctx,
		`SELECT DISTINCT path FROM ai_memory_chunks
         WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3 AND source=$4`+genSQL,
		args...,
	)
	if err != nil {
		return err
	}
	defer rows.Close()

	var stalePaths []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return err
		}
		if _, ok := active[path]; !ok {
			stalePaths = append(stalePaths, path)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, path := range stalePaths {
		delGenSQL, delGenArgs := generationFilterSQL(6, generation)
		delArgs := append([]any{m.bridgeID, m.loginID, m.agentID, path, source}, delGenArgs...)
		if _, err := m.db.Exec(ctx,
			`DELETE FROM ai_memory_chunks
             WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3 AND path=$4 AND source=$5`+delGenSQL,
			delArgs...,
		); err != nil {
			m.log.Warn().Err(err).Str("path", path).Msg("stale chunk delete failed")
		}
		if m.ftsAvailable {
			if _, err := m.db.Exec(ctx,
				`DELETE FROM ai_memory_chunks_fts
                 WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3 AND path=$4 AND source=$5`+delGenSQL,
				delArgs...,
			); err != nil {
				m.log.Warn().Err(err).Str("path", path).Msg("stale FTS delete failed")
			}
		}
	}
	return nil
}

// collectOldGenerationIDs returns chunk IDs that belong to older generations.
func (m *MemorySearchManager) collectOldGenerationIDs(ctx context.Context, generation string) []string {
	generation = strings.TrimSpace(generation)
	if generation == "" {
		return nil
	}
	rows, err := m.db.Query(ctx,
		`SELECT id FROM ai_memory_chunks
         WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3 AND id NOT LIKE $4`,
		m.bridgeID, m.loginID, m.agentID, generation+":%",
	)
	if err != nil {
		return nil
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return ids
		}
		ids = append(ids, id)
	}
	rows.Close()
	return ids
}

// deleteOldGenerations removes chunks and FTS entries from previous generations.
func (m *MemorySearchManager) deleteOldGenerations(ctx context.Context, generation string) {
	generation = strings.TrimSpace(generation)
	if generation == "" {
		return
	}
	if m.ftsAvailable {
		ids := m.collectOldGenerationIDs(ctx, generation)
		for _, id := range ids {
			if _, err := m.db.Exec(ctx,
				`DELETE FROM ai_memory_chunks_fts
                 WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3 AND id=$4`,
				m.bridgeID, m.loginID, m.agentID, id,
			); err != nil {
				m.log.Warn().Err(err).Str("chunk_id", id).Msg("old generation FTS delete failed")
			}
		}
	}
	if _, err := m.db.Exec(ctx,
		`DELETE FROM ai_memory_chunks
         WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3 AND id NOT LIKE $4`,
		m.bridgeID, m.loginID, m.agentID, generation+":%",
	); err != nil {
		m.log.Warn().Err(err).Str("generation", generation).Msg("old generation chunk delete failed")
	}
}

func (m *MemorySearchManager) searchKeyword(ctx context.Context, query string, limit int, sources []string, pathPrefix string, indexGen string) ([]memorycore.HybridKeywordResult, error) {
	if !m.ftsAvailable || limit <= 0 {
		return nil, nil
	}
	ftsQuery := memorycore.BuildFtsQuery(query)
	if ftsQuery == "" {
		return nil, nil
	}
	baseArgs := []any{ftsQuery, m.status.Model, m.bridgeID, m.loginID, m.agentID}
	filterSQL, filterArgs := sourceFilterSQL(6, sources)
	genSQL, genArgs := generationFilterSQL(6+len(filterArgs), indexGen)
	pathSQL, pathArgs := pathPrefixFilterSQL(6+len(filterArgs)+len(genArgs), pathPrefix)
	args := append(baseArgs, filterArgs...)
	args = append(args, genArgs...)
	args = append(args, pathArgs...)
	rows, err := m.db.Query(ctx,
		`SELECT id, path, source, start_line, end_line, text,
           bm25(ai_memory_chunks_fts) AS rank
         FROM ai_memory_chunks_fts
         WHERE ai_memory_chunks_fts MATCH $1 AND model=$2 AND bridge_id=$3 AND login_id=$4 AND agent_id=$5`+filterSQL+genSQL+pathSQL+`
         ORDER BY rank ASC
         LIMIT $`+fmt.Sprintf("%d", len(args)+1),
		append(args, limit)...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []memorycore.HybridKeywordResult
	for rows.Next() {
		var id, path, source, text string
		var startLine, endLine int
		var rank float64
		if err := rows.Scan(&id, &path, &source, &startLine, &endLine, &text, &rank); err != nil {
			return nil, err
		}
		results = append(results, memorycore.HybridKeywordResult{
			ID:        id,
			Path:      path,
			StartLine: startLine,
			EndLine:   endLine,
			Source:    source,
			Snippet:   truncateSnippet(text),
			TextScore: memorycore.BM25RankToScore(rank),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

func sourceFilterSQL(startIndex int, sources []string) (string, []any) {
	if len(sources) == 0 {
		return "", nil
	}
	placeholders := make([]string, 0, len(sources))
	args := make([]any, 0, len(sources))
	for i, source := range sources {
		placeholders = append(placeholders, fmt.Sprintf("$%d", startIndex+i))
		args = append(args, source)
	}
	return " AND source IN (" + strings.Join(placeholders, ",") + ")", args
}

func pathPrefixFilterSQL(startIndex int, prefix string) (string, []any) {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return "", nil
	}
	return fmt.Sprintf(" AND (path=$%d OR path LIKE $%d)", startIndex, startIndex+1), []any{prefix, prefix + "/%"}
}

func generationFilterSQL(startIndex int, generation string) (string, []any) {
	generation = strings.TrimSpace(generation)
	if generation == "" {
		return "", nil
	}
	return fmt.Sprintf(" AND id LIKE $%d", startIndex), []any{generation + ":%"}
}

func hasSource(sources []string, target string) bool {
	return slices.Contains(sources, target)
}

func isExtraPath(path string, extra []string) bool {
	for _, extraPath := range extra {
		if ok, _, _ := textfs.IsAllowedTextNotePath(extraPath); ok {
			if strings.EqualFold(path, extraPath) {
				return true
			}
			continue
		}
		if path == extraPath || strings.HasPrefix(path, extraPath+"/") {
			return true
		}
	}
	return false
}
