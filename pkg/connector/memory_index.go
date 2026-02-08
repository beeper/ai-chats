package connector

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/beeper/ai-bridge/pkg/memory"
	"github.com/beeper/ai-bridge/pkg/memory/embedding"
	"github.com/beeper/ai-bridge/pkg/textfs"
)

func (m *MemorySearchManager) ensureSchema(ctx context.Context) {
	if m == nil || m.db == nil {
		return
	}
	if !m.cfg.Query.Hybrid.Enabled {
		m.ftsAvailable = false
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
}

// syncWithProgress is like sync but calls onProgress during indexing.
// The progress callback is set before acquiring the sync lock and cleared after.
func (m *MemorySearchManager) syncWithProgress(ctx context.Context, sessionKey string, force bool, onProgress func(completed, total int, label string)) error {
	if m == nil {
		return errors.New("memory search unavailable")
	}
	// Safe: syncProgress is only read inside sync() which holds m.mu.
	// This write happens before sync() acquires the lock, creating a happens-before.
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
	generation := m.indexGen
	if needsFullReindex || generation == "" {
		generation = uuid.NewString()
	}

	if needsFullReindex {
		return m.syncFullReindex(ctx, sessionKey, generation)
	}
	return m.syncIncremental(ctx, sessionKey, generation)
}

// syncFullReindex wraps a full reindex in a database transaction for atomicity.
// If any step fails, the transaction rolls back and the existing index remains intact.
// Vector table operations (separate sql.Conn) are best-effort outside the transaction.
func (m *MemorySearchManager) syncFullReindex(ctx context.Context, sessionKey, generation string) error {
	// Collect vector IDs to clean up after commit (vector table uses separate conn).
	var vectorCleanupIDs []string

	err := m.db.DoTxn(ctx, nil, func(txCtx context.Context) error {
		if err := m.indexMemoryFiles(txCtx, true, generation); err != nil {
			return err
		}
		if m.cfg.Experimental.SessionMemory && hasSource(m.cfg.Sources, "sessions") {
			if err := m.syncSessions(txCtx, true, sessionKey, generation); err != nil {
				return err
			}
		}
		if err := m.updateMeta(txCtx, generation); err != nil {
			return err
		}
		// Collect old-generation chunk IDs for vector cleanup (within txn so we see consistent state).
		vectorCleanupIDs = m.collectOldGenerationIDs(txCtx, generation)
		// Delete old-generation chunks and FTS entries within the transaction.
		m.deleteOldGenerations(txCtx, generation)
		return nil
	})
	if err != nil {
		return err
	}

	// Transaction committed successfully — update in-memory state.
	m.indexGen = generation
	if hasSource(m.cfg.Sources, "memory") {
		m.dirty = false
	}
	if m.cfg.Experimental.SessionMemory && hasSource(m.cfg.Sources, "sessions") {
		m.sessionsDirty = false
	}

	// Best-effort vector cleanup outside the transaction.
	if m.vectorReady && len(vectorCleanupIDs) > 0 {
		m.deleteVectorIDs(ctx, vectorCleanupIDs)
	}
	return nil
}

// syncIncremental performs an incremental (non-transactional) sync for unchanged config.
func (m *MemorySearchManager) syncIncremental(ctx context.Context, sessionKey, generation string) error {
	if err := m.indexMemoryFiles(ctx, false, generation); err != nil {
		return err
	}
	if hasSource(m.cfg.Sources, "memory") {
		m.dirty = false
	}

	if m.cfg.Experimental.SessionMemory && hasSource(m.cfg.Sources, "sessions") {
		if err := m.syncSessions(ctx, false, sessionKey, generation); err != nil {
			return err
		}
		m.sessionsDirty = false
	}

	return m.updateMeta(ctx, generation)
}

func (m *MemorySearchManager) needsFullReindex(ctx context.Context, force bool) (bool, error) {
	if force {
		return true, nil
	}
	var provider, model, providerKey, indexGen string
	var chunkTokens, chunkOverlap int
	var vectorDims sql.NullInt64
	row := m.db.QueryRow(ctx,
		`SELECT provider, model, provider_key, chunk_tokens, chunk_overlap, vector_dims, index_generation
         FROM ai_memory_meta
         WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3`,
		m.bridgeID, m.loginID, m.agentID,
	)
	switch err := row.Scan(&provider, &model, &providerKey, &chunkTokens, &chunkOverlap, &vectorDims, &indexGen); err {
	case nil:
		if provider != m.status.Provider ||
			model != m.status.Model ||
			providerKey != m.providerKey ||
			chunkTokens != m.cfg.Chunking.Tokens ||
			chunkOverlap != m.cfg.Chunking.Overlap {
			return true, nil
		}
		if vectorDims.Valid {
			m.vectorDims = int(vectorDims.Int64)
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
	vectorDims := m.vectorDims
	_, err := m.db.Exec(ctx,
		`INSERT INTO ai_memory_meta
           (bridge_id, login_id, agent_id, provider, model, provider_key, chunk_tokens, chunk_overlap, vector_dims, index_generation, updated_at)
         VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
         ON CONFLICT (bridge_id, login_id, agent_id)
         DO UPDATE SET provider=excluded.provider, model=excluded.model, provider_key=excluded.provider_key,
           chunk_tokens=excluded.chunk_tokens, chunk_overlap=excluded.chunk_overlap, vector_dims=excluded.vector_dims,
           index_generation=excluded.index_generation, updated_at=excluded.updated_at`,
		m.bridgeID, m.loginID, m.agentID,
		m.status.Provider, m.status.Model, m.providerKey,
		m.cfg.Chunking.Tokens, m.cfg.Chunking.Overlap,
		vectorDimsOrNull(vectorDims),
		generation,
		time.Now().UnixMilli(),
	)
	return err
}

func vectorDimsOrNull(value int) any {
	if value <= 0 {
		return nil
	}
	return value
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
	store := textfs.NewStore(m.db, m.bridgeID, m.loginID, m.agentID)
	entries, err := store.List(ctx)
	if err != nil {
		return err
	}
	extraPaths := normalizeExtraPaths(m.cfg.ExtraPaths)
	activePaths := make(map[string]textfs.FileEntry)

	for _, entry := range entries {
		path := strings.TrimSpace(entry.Path)
		if path == "" {
			continue
		}
		if !strings.HasSuffix(strings.ToLower(path), ".md") {
			continue
		}
		if !textfs.IsMemoryPath(path) && !isExtraPath(path, extraPaths) {
			continue
		}
		activePaths[path] = entry
	}

	m.log.Debug().
		Int("files", len(activePaths)).
		Bool("needsFullReindex", force).
		Bool("batch", m.batchEnabled).
		Int("concurrency", m.indexConcurrency()).
		Msg("memory sync: indexing memory files")

	total := len(activePaths)
	completed := 0
	for _, entry := range activePaths {
		source := "memory"
		needs, err := m.needsFileIndex(ctx, entry, source, generation)
		if err != nil {
			return err
		}
		if !force && !needs {
			completed++
			continue
		}
		if m.syncProgress != nil {
			m.syncProgress(completed, total, entry.Path)
		}
		if err := m.indexContent(ctx, entry.Path, source, entry.Content, generation); err != nil {
			return err
		}
		completed++
	}
	if m.syncProgress != nil {
		m.syncProgress(total, total, "cleanup")
	}

	return m.removeStaleMemoryChunks(ctx, activePaths, generation)
}

func (m *MemorySearchManager) indexConcurrency() int {
	if m == nil {
		return 1
	}
	if m.batchEnabled && m.cfg != nil && m.cfg.Remote.Batch.Concurrency > 0 {
		return m.cfg.Remote.Batch.Concurrency
	}
	if embeddingIndexConcurrency > 0 {
		return embeddingIndexConcurrency
	}
	return 1
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

func (m *MemorySearchManager) indexContent(ctx context.Context, path, source, content, generation string) error {
	cleanContent := normalizeNewlines(content)
	chunks := memory.ChunkMarkdown(cleanContent, m.cfg.Chunking.Tokens, m.cfg.Chunking.Overlap)
	filtered := chunks[:0]
	for _, chunk := range chunks {
		if strings.TrimSpace(chunk.Text) == "" {
			continue
		}
		filtered = append(filtered, chunk)
	}
	chunks = filtered
	if len(chunks) == 0 {
		return nil
	}

	var embeddings [][]float64
	if m.cfg.Store.Vector.Enabled {
		var err error
		embeddings, err = m.embedChunks(ctx, chunks, path, source)
		if err != nil {
			return err
		}
	} else {
		embeddings = make([][]float64, len(chunks))
	}
	now := time.Now().UnixMilli()
	vectorReady := false
	newIDs := make([]string, 0, len(chunks))
	for i, chunk := range chunks {
		embedding := []float64{}
		if i < len(embeddings) {
			embedding = embeddings[i]
		}
		if m.cfg.Store.Vector.Enabled && m.vectorDims == 0 && len(embedding) > 0 {
			m.vectorDims = len(embedding)
		}
		if !vectorReady && m.cfg.Store.Vector.Enabled && len(embedding) > 0 {
			vectorReady = m.ensureVectorTable(ctx, len(embedding))
		}
		embeddingJSON, _ := json.Marshal(embedding)
		chunkID := buildChunkID(generation)
		newIDs = append(newIDs, chunkID)
		_, err := m.db.Exec(ctx,
			`INSERT INTO ai_memory_chunks
             (id, bridge_id, login_id, agent_id, path, source, start_line, end_line, hash, model, text, embedding, updated_at)
             VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
			chunkID, m.bridgeID, m.loginID, m.agentID, path, source, chunk.StartLine, chunk.EndLine, chunk.Hash,
			m.status.Model, chunk.Text, string(embeddingJSON), now,
		)
		if err != nil {
			return err
		}
		if vectorReady && len(embedding) > 0 {
			_, _ = m.execVector(ctx, fmt.Sprintf("DELETE FROM %s WHERE id=?", memoryVectorTable), chunkID)
			_, _ = m.execVector(ctx,
				fmt.Sprintf("INSERT INTO %s (id, embedding) VALUES (?, ?)", memoryVectorTable),
				chunkID, vectorToBlob(embedding),
			)
		}
		if m.ftsAvailable {
			_, _ = m.db.Exec(ctx,
				`INSERT INTO ai_memory_chunks_fts
                 (text, id, path, source, model, start_line, end_line, bridge_id, login_id, agent_id)
                 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
				chunk.Text, chunkID, path, source, m.status.Model, chunk.StartLine, chunk.EndLine, m.bridgeID, m.loginID, m.agentID,
			)
		}
	}
	if err := m.deletePathChunks(ctx, path, source, generation, newIDs); err != nil {
		return err
	}

	return nil
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
	if m.vectorReady {
		m.deleteVectorIDs(ctx, ids)
	}
	for _, id := range ids {
		_, _ = m.db.Exec(ctx,
			`DELETE FROM ai_memory_chunks_fts
             WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3 AND id=$4`,
			m.bridgeID, m.loginID, m.agentID, id,
		)
	}
	_, err = m.db.Exec(ctx,
		`DELETE FROM ai_memory_chunks
         WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3 AND path=$4 AND source=$5 AND model=$6`+genFilter+placeholders,
		args...,
	)
	return err
}

func (m *MemorySearchManager) removeStaleMemoryChunks(ctx context.Context, active map[string]textfs.FileEntry, generation string) error {
	genSQL, genArgs := generationFilterSQL(5, generation)
	args := []any{m.bridgeID, m.loginID, m.agentID, "memory"}
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
		if m.vectorReady {
			ids := m.collectChunkIDs(ctx, path, "memory", m.status.Model, generation)
			m.deleteVectorIDs(ctx, ids)
		}
		_, _ = m.db.Exec(ctx,
			`DELETE FROM ai_memory_chunks
             WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3 AND path=$4 AND source=$5`+delGenSQL,
			append([]any{m.bridgeID, m.loginID, m.agentID, path, "memory"}, delGenArgs...)...,
		)
		if m.ftsAvailable {
			_, _ = m.db.Exec(ctx,
				`DELETE FROM ai_memory_chunks_fts
                 WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3 AND path=$4 AND source=$5`+delGenSQL,
				append([]any{m.bridgeID, m.loginID, m.agentID, path, "memory"}, delGenArgs...)...,
			)
		}
	}
	return nil
}

// collectOldGenerationIDs returns chunk IDs that belong to older generations.
// Used to collect IDs for vector table cleanup before deleting from main tables.
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
// Safe to call inside a transaction — vector cleanup should be done separately.
func (m *MemorySearchManager) deleteOldGenerations(ctx context.Context, generation string) {
	generation = strings.TrimSpace(generation)
	if generation == "" {
		return
	}
	if m.ftsAvailable {
		ids := m.collectOldGenerationIDs(ctx, generation)
		for _, id := range ids {
			_, _ = m.db.Exec(ctx,
				`DELETE FROM ai_memory_chunks_fts
                 WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3 AND id=$4`,
				m.bridgeID, m.loginID, m.agentID, id,
			)
		}
	}
	_, _ = m.db.Exec(ctx,
		`DELETE FROM ai_memory_chunks
         WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3 AND id NOT LIKE $4`,
		m.bridgeID, m.loginID, m.agentID, generation+":%",
	)
}

type missingChunk struct {
	index int
	chunk memory.Chunk
}

func (m *MemorySearchManager) embedChunks(ctx context.Context, chunks []memory.Chunk, relPath, source string) ([][]float64, error) {
	embeddings := make([][]float64, len(chunks))
	var missing []missingChunk
	for i, chunk := range chunks {
		if chunk.Text == "" {
			continue
		}
		if m.cfg.Cache.Enabled {
			if cached, ok := m.lookupEmbeddingCache(ctx, chunk.Hash); ok {
				embeddings[i] = cached
				continue
			}
		}
		missing = append(missing, missingChunk{index: i, chunk: chunk})
	}
	if len(missing) == 0 {
		return embeddings, nil
	}

	if m.shouldUseBatch(m.status.Provider) {
		switch m.status.Provider {
		case "openai":
			batchResults, err := m.runBatchWithTimeoutRetry("openai", func() (map[string][]float64, error) {
				return m.embedChunksWithOpenAIBatch(ctx, missing, relPath, source)
			})
			if err == nil {
				for _, item := range missing {
					customID := batchCustomID(source, relPath, item.chunk.Hash, item.chunk.StartLine, item.chunk.EndLine, item.index)
					if embedding, ok := batchResults[customID]; ok {
						embeddings[item.index] = embedding
						if m.cfg.Cache.Enabled {
							_ = m.storeEmbeddingCache(ctx, item.chunk.Hash, embedding)
						}
					}
				}
				m.resetBatchFailures()
				return embeddings, nil
			}
			message := err.Error()
			forceDisable := strings.Contains(strings.ToLower(message), "asyncbatchembedcontent not available")
			disabled, count := m.recordBatchFailure("openai", err, batchAttempts(err), forceDisable)
			suffix := "keeping batch enabled"
			if disabled {
				suffix = "disabling batch"
			}
			m.log.Warn().Msg(fmt.Sprintf(
				"memory embeddings: openai batch failed (%d/%d); %s; falling back to non-batch embeddings: %s",
				count, batchFailureLimit, suffix, message,
			))
		case "gemini":
			batchResults, err := m.runBatchWithTimeoutRetry("gemini", func() (map[string][]float64, error) {
				return m.embedChunksWithGeminiBatch(ctx, missing, relPath, source)
			})
			if err == nil {
				for _, item := range missing {
					customID := batchCustomID(source, relPath, item.chunk.Hash, item.chunk.StartLine, item.chunk.EndLine, item.index)
					if embedding, ok := batchResults[customID]; ok {
						embeddings[item.index] = embedding
						if m.cfg.Cache.Enabled {
							_ = m.storeEmbeddingCache(ctx, item.chunk.Hash, embedding)
						}
					}
				}
				m.resetBatchFailures()
				return embeddings, nil
			}
			message := err.Error()
			forceDisable := strings.Contains(strings.ToLower(message), "asyncbatchembedcontent not available")
			disabled, count := m.recordBatchFailure("gemini", err, batchAttempts(err), forceDisable)
			suffix := "keeping batch enabled"
			if disabled {
				suffix = "disabling batch"
			}
			m.log.Warn().Msg(fmt.Sprintf(
				"memory embeddings: gemini batch failed (%d/%d); %s; falling back to non-batch embeddings: %s",
				count, batchFailureLimit, suffix, message,
			))
		}
	}

	missingChunks := make([]memory.Chunk, len(missing))
	for i, item := range missing {
		missingChunks[i] = item.chunk
	}
	results, err := m.embedChunksInBatches(ctx, missingChunks)
	if err != nil {
		return nil, err
	}
	for i, item := range missing {
		if i < len(results) {
			embeddings[item.index] = results[i]
		}
	}
	return embeddings, nil
}

func (m *MemorySearchManager) embedChunksWithOpenAIBatch(
	ctx context.Context,
	missing []missingChunk,
	relPath string,
	source string,
) (map[string][]float64, error) {
	if m == nil || m.client == nil {
		return nil, errors.New("memory search unavailable")
	}
	apiKey, baseURL, headers := resolveOpenAIEmbeddingConfig(m.client, m.cfg)
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("openai embeddings require api_key")
	}
	if strings.TrimSpace(baseURL) == "" {
		baseURL = embedding.DefaultOpenAIBaseURL
	}
	requests, _ := buildOpenAIRequests(relPath, source, m.status.Model, missing)
	params := openAIBatchParams{
		BaseURL:      baseURL,
		APIKey:       apiKey,
		Headers:      headers,
		AgentID:      m.agentID,
		Requests:     requests,
		Wait:         m.cfg.Remote.Batch.Wait,
		PollInterval: time.Duration(m.cfg.Remote.Batch.PollIntervalMs) * time.Millisecond,
		Timeout:      time.Duration(m.cfg.Remote.Batch.TimeoutMinutes) * time.Minute,
		Concurrency:  m.cfg.Remote.Batch.Concurrency,
		Client:       &http.Client{Timeout: time.Duration(m.cfg.Remote.Batch.TimeoutMinutes) * time.Minute},
	}
	if params.PollInterval <= 0 {
		params.PollInterval = 2 * time.Second
	}
	if params.Timeout <= 0 {
		params.Timeout = 60 * time.Minute
	}
	return runOpenAIBatches(ctx, params)
}

func (m *MemorySearchManager) embedChunksWithGeminiBatch(
	ctx context.Context,
	missing []missingChunk,
	relPath string,
	source string,
) (map[string][]float64, error) {
	if m == nil {
		return nil, errors.New("memory search unavailable")
	}
	apiKey, baseURL, headers := resolveGeminiEmbeddingConfig(m.client, m.cfg)
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("gemini embeddings require api_key")
	}
	if strings.TrimSpace(baseURL) == "" {
		baseURL = embedding.DefaultGeminiBaseURL
	}
	requests, _ := buildGeminiRequests(relPath, source, missing)
	params := geminiBatchParams{
		BaseURL:      baseURL,
		APIKey:       apiKey,
		Headers:      headers,
		AgentID:      m.agentID,
		Model:        m.status.Model,
		Requests:     requests,
		Wait:         m.cfg.Remote.Batch.Wait,
		PollInterval: time.Duration(m.cfg.Remote.Batch.PollIntervalMs) * time.Millisecond,
		Timeout:      time.Duration(m.cfg.Remote.Batch.TimeoutMinutes) * time.Minute,
		Concurrency:  m.cfg.Remote.Batch.Concurrency,
		Client:       &http.Client{Timeout: time.Duration(m.cfg.Remote.Batch.TimeoutMinutes) * time.Minute},
	}
	if params.PollInterval <= 0 {
		params.PollInterval = 2 * time.Second
	}
	if params.Timeout <= 0 {
		params.Timeout = 60 * time.Minute
	}
	return runGeminiBatches(ctx, params)
}

func (m *MemorySearchManager) lookupEmbeddingCache(ctx context.Context, hash string) ([]float64, bool) {
	var raw string
	row := m.db.QueryRow(ctx,
		`SELECT embedding FROM ai_memory_embedding_cache
         WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3 AND provider=$4 AND model=$5 AND provider_key=$6 AND hash=$7`,
		m.bridgeID, m.loginID, m.agentID, m.status.Provider, m.status.Model, m.providerKey, hash,
	)
	if err := row.Scan(&raw); err != nil {
		return nil, false
	}
	embedding := parseEmbedding(raw)
	if len(embedding) == 0 {
		return nil, false
	}
	return embedding, true
}

func (m *MemorySearchManager) storeEmbeddingCache(ctx context.Context, hash string, embedding []float64) error {
	if len(embedding) == 0 {
		return nil
	}
	raw, _ := json.Marshal(embedding)
	dims := len(embedding)
	_, err := m.db.Exec(ctx,
		`INSERT INTO ai_memory_embedding_cache
         (bridge_id, login_id, agent_id, provider, model, provider_key, hash, embedding, dims, updated_at)
         VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
         ON CONFLICT (bridge_id, login_id, agent_id, provider, model, provider_key, hash)
         DO UPDATE SET embedding=excluded.embedding, dims=excluded.dims, updated_at=excluded.updated_at`,
		m.bridgeID, m.loginID, m.agentID, m.status.Provider, m.status.Model, m.providerKey, hash, string(raw), dims, time.Now().UnixMilli(),
	)
	if err != nil {
		return err
	}
	maybePruneEmbeddingCache(ctx, m)
	return nil
}

func maybePruneEmbeddingCache(ctx context.Context, m *MemorySearchManager) {
	if m.cfg.Cache.MaxEntries <= 0 {
		return
	}
	var count int
	row := m.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM ai_memory_embedding_cache
         WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3 AND provider=$4 AND model=$5 AND provider_key=$6`,
		m.bridgeID, m.loginID, m.agentID, m.status.Provider, m.status.Model, m.providerKey,
	)
	if err := row.Scan(&count); err != nil {
		return
	}
	if count <= m.cfg.Cache.MaxEntries {
		return
	}
	toRemove := count - m.cfg.Cache.MaxEntries
	_, _ = m.db.Exec(ctx,
		`DELETE FROM ai_memory_embedding_cache
         WHERE rowid IN (
           SELECT rowid FROM ai_memory_embedding_cache
           WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3 AND provider=$4 AND model=$5 AND provider_key=$6
           ORDER BY updated_at ASC
           LIMIT $7
         )`,
		m.bridgeID, m.loginID, m.agentID, m.status.Provider, m.status.Model, m.providerKey, toRemove,
	)
}

func (m *MemorySearchManager) searchVector(ctx context.Context, queryVec []float64, limit int) ([]memory.HybridVectorResult, error) {
	if !m.cfg.Store.Vector.Enabled {
		return nil, nil
	}
	if len(queryVec) == 0 || limit <= 0 {
		return nil, nil
	}
	if m.ensureVectorTable(ctx, len(queryVec)) {
		filterSQL, filterArgs := sourceFilterSQLQuestion(m.cfg.Sources)
		genSQL, genArgs := generationFilterSQLQuestion(m.indexGen)
		args := []any{vectorToBlob(queryVec), m.bridgeID, m.loginID, m.agentID, m.status.Model}
		args = append(args, filterArgs...)
		args = append(args, genArgs...)
		args = append(args, limit)
		rows, err := m.queryVector(ctx,
			fmt.Sprintf(`SELECT c.id, c.path, c.start_line, c.end_line, c.text, c.source,
               vec_distance_cosine(v.embedding, ?) AS dist
             FROM %s v
             JOIN ai_memory_chunks c ON c.id = v.id
             WHERE c.bridge_id=? AND c.login_id=? AND c.agent_id=? AND c.model=?%s%s
             ORDER BY dist ASC
             LIMIT ?`, memoryVectorTable, filterSQL, genSQL),
			args...,
		)
		if err == nil {
			defer rows.Close()
			results := make([]memory.HybridVectorResult, 0, limit)
			for rows.Next() {
				var id, path, text, source string
				var startLine, endLine int
				var dist float64
				if err := rows.Scan(&id, &path, &startLine, &endLine, &text, &source, &dist); err != nil {
					return nil, err
				}
				results = append(results, memory.HybridVectorResult{
					ID:          id,
					Path:        path,
					StartLine:   startLine,
					EndLine:     endLine,
					Source:      source,
					Snippet:     truncateSnippet(text),
					VectorScore: 1 - dist,
				})
			}
			if err := rows.Err(); err == nil {
				return results, nil
			}
		} else {
			m.mu.Lock()
			m.vectorError = err.Error()
			m.mu.Unlock()
		}
	}

	baseArgs := []any{m.bridgeID, m.loginID, m.agentID, m.status.Model}
	filterSQL, filterArgs := sourceFilterSQL(5, m.cfg.Sources)
	genSQL, genArgs := generationFilterSQL(5+len(filterArgs), m.indexGen)
	args := append(baseArgs, filterArgs...)
	args = append(args, genArgs...)
	rows, err := m.db.Query(ctx,
		`SELECT id, path, start_line, end_line, text, embedding, source
         FROM ai_memory_chunks
         WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3 AND model=$4`+filterSQL+genSQL,
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type scored struct {
		result memory.HybridVectorResult
		score  float64
	}
	var scoredResults []scored
	for rows.Next() {
		var id, path, text, embeddingRaw, source string
		var startLine, endLine int
		if err := rows.Scan(&id, &path, &startLine, &endLine, &text, &embeddingRaw, &source); err != nil {
			return nil, err
		}
		embedding := parseEmbedding(embeddingRaw)
		score := cosineSimilarity(queryVec, embedding)
		scoredResults = append(scoredResults, scored{
			result: memory.HybridVectorResult{
				ID:          id,
				Path:        path,
				StartLine:   startLine,
				EndLine:     endLine,
				Source:      source,
				Snippet:     truncateSnippet(text),
				VectorScore: score,
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
	results := make([]memory.HybridVectorResult, 0, len(scoredResults))
	for _, entry := range scoredResults {
		results = append(results, entry.result)
	}
	return results, nil
}

func (m *MemorySearchManager) searchKeyword(ctx context.Context, query string, limit int) ([]memory.HybridKeywordResult, error) {
	if !m.ftsAvailable || limit <= 0 {
		return nil, nil
	}
	ftsQuery := memory.BuildFtsQuery(query)
	if ftsQuery == "" {
		return nil, nil
	}
	baseArgs := []any{ftsQuery, m.status.Model, m.bridgeID, m.loginID, m.agentID}
	filterSQL, filterArgs := sourceFilterSQL(6, m.cfg.Sources)
	genSQL, genArgs := generationFilterSQL(6+len(filterArgs), m.indexGen)
	args := append(baseArgs, filterArgs...)
	args = append(args, genArgs...)
	rows, err := m.db.Query(ctx,
		`SELECT id, path, source, start_line, end_line, text,
           bm25(ai_memory_chunks_fts) AS rank
         FROM ai_memory_chunks_fts
         WHERE ai_memory_chunks_fts MATCH $1 AND model=$2 AND bridge_id=$3 AND login_id=$4 AND agent_id=$5`+filterSQL+genSQL+`
         ORDER BY rank ASC
         LIMIT $`+fmt.Sprintf("%d", len(args)+1),
		append(args, limit)...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []memory.HybridKeywordResult
	for rows.Next() {
		var id, path, source, text string
		var startLine, endLine int
		var rank float64
		if err := rows.Scan(&id, &path, &source, &startLine, &endLine, &text, &rank); err != nil {
			return nil, err
		}
		score := memory.BM25RankToScore(rank)
		results = append(results, memory.HybridKeywordResult{
			ID:        id,
			Path:      path,
			StartLine: startLine,
			EndLine:   endLine,
			Source:    source,
			Snippet:   truncateSnippet(text),
			TextScore: score,
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

func sourceFilterSQLQuestion(sources []string) (string, []any) {
	if len(sources) == 0 {
		return "", nil
	}
	placeholders := make([]string, 0, len(sources))
	args := make([]any, 0, len(sources))
	for _, source := range sources {
		placeholders = append(placeholders, "?")
		args = append(args, source)
	}
	return " AND c.source IN (" + strings.Join(placeholders, ",") + ")", args
}

func generationFilterSQL(startIndex int, generation string) (string, []any) {
	generation = strings.TrimSpace(generation)
	if generation == "" {
		return "", nil
	}
	return fmt.Sprintf(" AND id LIKE $%d", startIndex), []any{generation + ":%"}
}

func generationFilterSQLQuestion(generation string) (string, []any) {
	generation = strings.TrimSpace(generation)
	if generation == "" {
		return "", nil
	}
	return " AND c.id LIKE ?", []any{generation + ":%"}
}

func parseEmbedding(raw string) []float64 {
	var values []float64
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil
	}
	return values
}

func cosineSimilarity(a, b []float64) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	length := len(a)
	if len(b) < length {
		length = len(b)
	}
	var dot, normA, normB float64
	for i := 0; i < length; i++ {
		av := a[i]
		bv := b[i]
		dot += av * bv
		normA += av * av
		normB += bv * bv
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

func (m *MemorySearchManager) collectChunkIDs(ctx context.Context, path, source, model, generation string) []string {
	if m == nil {
		return nil
	}
	genSQL, genArgs := generationFilterSQL(7, generation)
	args := []any{m.bridgeID, m.loginID, m.agentID, path, source, model}
	args = append(args, genArgs...)
	rows, err := m.db.Query(ctx,
		`SELECT id FROM ai_memory_chunks
         WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3 AND path=$4 AND source=$5 AND model=$6`+genSQL,
		args...,
	)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return ids
		}
		ids = append(ids, id)
	}
	return ids
}

func hasSource(sources []string, target string) bool {
	return slices.Contains(sources, target)
}

func isExtraPath(path string, extra []string) bool {
	for _, extraPath := range extra {
		if strings.HasSuffix(strings.ToLower(extraPath), ".md") {
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
