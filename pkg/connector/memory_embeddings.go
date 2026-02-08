package connector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"regexp"
	"strings"
	"time"

	"github.com/beeper/ai-bridge/pkg/memory"
)

const (
	embeddingBatchMaxTokens      = 8000
	embeddingApproxCharsPerToken = 1
	embeddingIndexConcurrency    = 4
	embeddingRetryMaxAttempts    = 3
	embeddingRetryBaseDelay      = 500 * time.Millisecond
	embeddingRetryMaxDelay       = 8 * time.Second
	embeddingQueryTimeoutRemote  = 60 * time.Second
	embeddingBatchTimeoutRemote  = 2 * time.Minute
)

var retryableEmbeddingRE = regexp.MustCompile(`(?i)(rate[_ ]limit|too many requests|429|resource has been exhausted|5\\d\\d|cloudflare)`)

func (m *MemorySearchManager) estimateEmbeddingTokens(text string) int {
	if text == "" {
		return 0
	}
	return int(math.Ceil(float64(len(text)) / float64(embeddingApproxCharsPerToken)))
}

func (m *MemorySearchManager) buildEmbeddingBatches(chunks []memory.Chunk) [][]memory.Chunk {
	batches := make([][]memory.Chunk, 0)
	current := make([]memory.Chunk, 0)
	currentTokens := 0

	for _, chunk := range chunks {
		estimate := m.estimateEmbeddingTokens(chunk.Text)
		wouldExceed := len(current) > 0 && currentTokens+estimate > embeddingBatchMaxTokens
		if wouldExceed {
			batches = append(batches, current)
			current = nil
			currentTokens = 0
		}
		if len(current) == 0 && estimate > embeddingBatchMaxTokens {
			batches = append(batches, []memory.Chunk{chunk})
			continue
		}
		current = append(current, chunk)
		currentTokens += estimate
	}

	if len(current) > 0 {
		batches = append(batches, current)
	}
	return batches
}

func (m *MemorySearchManager) embedChunksInBatches(ctx context.Context, chunks []memory.Chunk) ([][]float64, error) {
	if len(chunks) == 0 {
		return nil, nil
	}
	embeddings := make([][]float64, len(chunks))
	cached := m.loadEmbeddingCache(ctx, chunks)
	missing := make([]missingChunk, 0, len(chunks))

	for i, chunk := range chunks {
		if chunk.Text == "" {
			continue
		}
		if cached != nil {
			if hit, ok := cached[chunk.Hash]; ok && len(hit) > 0 {
				embeddings[i] = hit
				continue
			}
		}
		missing = append(missing, missingChunk{index: i, chunk: chunk})
	}

	if len(missing) == 0 {
		return embeddings, nil
	}

	missingChunks := make([]memory.Chunk, len(missing))
	for i, item := range missing {
		missingChunks[i] = item.chunk
	}

	batches := m.buildEmbeddingBatches(missingChunks)
	cursor := 0
	toCache := make([]missingChunk, 0, len(missing))
	for _, batch := range batches {
		texts := make([]string, len(batch))
		for i, chunk := range batch {
			texts[i] = chunk.Text
		}
		batchEmbeddings, err := m.embedBatchWithRetry(ctx, texts)
		if err != nil {
			return nil, err
		}
		for i, chunk := range batch {
			item := missing[cursor+i]
			embedding := []float64{}
			if i < len(batchEmbeddings) {
				embedding = batchEmbeddings[i]
			}
			embeddings[item.index] = embedding
			toCache = append(toCache, missingChunk{index: item.index, chunk: memory.Chunk{
				StartLine: chunk.StartLine,
				EndLine:   chunk.EndLine,
				Text:      chunk.Text,
				Hash:      chunk.Hash,
			}})
		}
		cursor += len(batch)
	}
	m.storeEmbeddingCacheBatch(ctx, toCache, embeddings)
	return embeddings, nil
}

func (m *MemorySearchManager) embedBatchWithRetry(ctx context.Context, texts []string) ([][]float64, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	attempt := 0
	delay := embeddingRetryBaseDelay
	for {
		timeout := m.resolveEmbeddingTimeout("batch")
		m.log.Debug().Str("provider", m.status.Provider).Int("items", len(texts)).Int64("timeoutMs", timeout.Milliseconds()).
			Msg("memory embeddings: batch start")
		result, err := withTimeout(ctx, timeout, fmt.Sprintf(
			"memory embeddings batch timed out after %ds",
			int(math.Round(float64(timeout.Milliseconds())/1000.0)),
		), func(ctx context.Context) ([][]float64, error) {
			return m.provider.EmbedBatch(ctx, texts)
		})
		if err == nil {
			return result, nil
		}
		message := err.Error()
		if !m.isRetryableEmbeddingError(message) || attempt >= embeddingRetryMaxAttempts {
			return nil, err
		}
		wait := time.Duration(float64(delay) * (1 + rand.Float64()*0.2))
		if wait > embeddingRetryMaxDelay {
			wait = embeddingRetryMaxDelay
		}
		m.log.Warn().Msg(fmt.Sprintf("memory embeddings rate limited; retrying in %dms", wait.Milliseconds()))
		time.Sleep(wait)
		delay *= 2
		attempt++
	}
}

func (m *MemorySearchManager) embedQueryWithTimeout(ctx context.Context, text string) ([]float64, error) {
	timeout := m.resolveEmbeddingTimeout("query")
	m.log.Debug().Str("provider", m.status.Provider).Int64("timeoutMs", timeout.Milliseconds()).
		Msg("memory embeddings: query start")
	return withTimeout(ctx, timeout, fmt.Sprintf(
		"memory embeddings query timed out after %ds",
		int(math.Round(float64(timeout.Milliseconds())/1000.0)),
	), func(ctx context.Context) ([]float64, error) {
		return m.provider.EmbedQuery(ctx, text)
	})
}

func (m *MemorySearchManager) isRetryableEmbeddingError(message string) bool {
	return retryableEmbeddingRE.MatchString(message)
}

func (m *MemorySearchManager) resolveEmbeddingTimeout(kind string) time.Duration {
	if kind == "query" {
		return embeddingQueryTimeoutRemote
	}
	return embeddingBatchTimeoutRemote
}

func withTimeout[T any](ctx context.Context, timeout time.Duration, message string, fn func(context.Context) (T, error)) (T, error) {
	if timeout <= 0 {
		return fn(ctx)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result, err := fn(ctx)
	if err == nil {
		return result, nil
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		var zero T
		return zero, errors.New(message)
	}
	var zero T
	return zero, err
}

func (m *MemorySearchManager) loadEmbeddingCache(ctx context.Context, chunks []memory.Chunk) map[string][]float64 {
	if m == nil || !m.cfg.Cache.Enabled || len(chunks) == 0 {
		return nil
	}
	hashes := make([]string, 0, len(chunks))
	seen := make(map[string]struct{})
	for _, chunk := range chunks {
		if chunk.Hash == "" {
			continue
		}
		if _, ok := seen[chunk.Hash]; ok {
			continue
		}
		seen[chunk.Hash] = struct{}{}
		hashes = append(hashes, chunk.Hash)
	}
	if len(hashes) == 0 {
		return nil
	}
	out := make(map[string][]float64, len(hashes))
	baseArgs := []any{m.bridgeID, m.loginID, m.agentID, m.status.Provider, m.status.Model, m.providerKey}
	const batchSize = 400
	for start := 0; start < len(hashes); start += batchSize {
		end := start + batchSize
		if end > len(hashes) {
			end = len(hashes)
		}
		batch := hashes[start:end]
		placeholders := make([]string, 0, len(batch))
		args := append([]any{}, baseArgs...)
		for i, hash := range batch {
			placeholders = append(placeholders, fmt.Sprintf("$%d", len(baseArgs)+i+1))
			args = append(args, hash)
		}
		rows, err := m.db.Query(ctx,
			`SELECT hash, embedding FROM ai_memory_embedding_cache
             WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3 AND provider=$4 AND model=$5 AND provider_key=$6
               AND hash IN (`+strings.Join(placeholders, ",")+`)`,
			args...,
		)
		if err != nil {
			continue
		}
		for rows.Next() {
			var hash, raw string
			if err := rows.Scan(&hash, &raw); err == nil {
				out[hash] = parseEmbedding(raw)
			}
		}
		rows.Close()
	}
	return out
}

func (m *MemorySearchManager) storeEmbeddingCacheBatch(ctx context.Context, items []missingChunk, embeddings [][]float64) {
	if m == nil || !m.cfg.Cache.Enabled || len(items) == 0 {
		return
	}
	stmt := `INSERT INTO ai_memory_embedding_cache
    (bridge_id, login_id, agent_id, provider, model, provider_key, hash, embedding, dims, updated_at)
   VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
   ON CONFLICT (bridge_id, login_id, agent_id, provider, model, provider_key, hash)
   DO UPDATE SET embedding=excluded.embedding, dims=excluded.dims, updated_at=excluded.updated_at`
	now := time.Now().UnixMilli()
	for _, item := range items {
		idx := item.index
		embedding := []float64{}
		if idx >= 0 && idx < len(embeddings) {
			embedding = embeddings[idx]
		}
		raw, _ := json.Marshal(embedding)
		_, _ = m.db.Exec(ctx, stmt,
			m.bridgeID, m.loginID, m.agentID, m.status.Provider, m.status.Model, m.providerKey, item.chunk.Hash,
			string(raw), len(embedding), now,
		)
	}
	maybePruneEmbeddingCache(ctx, m)
}
