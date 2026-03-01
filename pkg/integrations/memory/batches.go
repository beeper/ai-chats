package memory

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/beeper/ai-bridge/pkg/shared/stringutil"
)

const (
	openAIBatchCompletionWindow = "24h"
	openAIBatchMaxRequests      = 50000
	geminiBatchMaxRequests      = 50000
	batchFailureLimit           = 2
)

type openAIBatchRequest struct {
	CustomID string `json:"custom_id"`
	Method   string `json:"method"`
	URL      string `json:"url"`
	Body     struct {
		Model string `json:"model"`
		Input string `json:"input"`
	} `json:"body"`
}

type openAIBatchStatus struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	OutputFile  string `json:"output_file_id"`
	ErrorFileID string `json:"error_file_id"`
}

type openAIBatchOutputLine struct {
	CustomID string `json:"custom_id"`
	Response struct {
		StatusCode int `json:"status_code"`
		Body       struct {
			Data []struct {
				Embedding []float64 `json:"embedding"`
			} `json:"data"`
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		} `json:"body"`
	} `json:"response"`
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

type geminiBatchRequest struct {
	CustomID string `json:"custom_id"`
	Content  struct {
		Parts []struct {
			Text string `json:"text"`
		} `json:"parts"`
	} `json:"content"`
	TaskType string `json:"taskType"`
}

type geminiBatchStatus struct {
	Name         string `json:"name"`
	State        string `json:"state"`
	OutputConfig struct {
		File   string `json:"file"`
		FileID string `json:"fileId"`
	} `json:"outputConfig"`
	Metadata struct {
		Output struct {
			ResponsesFile string `json:"responsesFile"`
		} `json:"output"`
	} `json:"metadata"`
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

type geminiBatchOutputLine struct {
	Key       string `json:"key"`
	CustomID  string `json:"custom_id"`
	RequestID string `json:"request_id"`
	Embedding struct {
		Values []float64 `json:"values"`
	} `json:"embedding"`
	Response struct {
		Embedding struct {
			Values []float64 `json:"values"`
		} `json:"embedding"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	} `json:"response"`
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

// shouldUseBatch reports whether batch embedding should be used.
// Caller must hold m.mu (called from embedChunks inside sync).
func (m *MemorySearchManager) shouldUseBatch(provider string) bool {
	if m == nil || m.cfg == nil || !m.cfg.Remote.Batch.Enabled {
		return false
	}
	if provider != "openai" && provider != "gemini" {
		return false
	}
	return m.batchEnabled
}

// resetBatchFailures clears batch failure state.
// Caller must hold m.mu (called from embedChunks inside sync).
func (m *MemorySearchManager) resetBatchFailures() {
	if m.batchFailures > 0 {
		m.log.Debug().Msg("memory embeddings: batch recovered; resetting failure count")
	}
	m.batchFailures = 0
	m.batchLastError = ""
	m.batchLastProvider = ""
}

type batchAttemptError struct {
	err      error
	attempts int
}

func (e *batchAttemptError) Error() string {
	if e == nil || e.err == nil {
		return ""
	}
	return e.err.Error()
}

func (e *batchAttemptError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func batchAttempts(err error) int {
	var attemptErr *batchAttemptError
	if errors.As(err, &attemptErr) && attemptErr.attempts > 0 {
		return attemptErr.attempts
	}
	return 1
}

func isBatchTimeoutError(message string) bool {
	return strings.Contains(strings.ToLower(message), "timed out") ||
		strings.Contains(strings.ToLower(message), "timeout")
}

func (m *MemorySearchManager) runBatchWithTimeoutRetry(provider string, run func() (map[string][]float64, error)) (map[string][]float64, error) {
	result, err := run()
	if err == nil {
		return result, nil
	}
	message := err.Error()
	if isBatchTimeoutError(message) {
		m.log.Warn().Msg(fmt.Sprintf("memory embeddings: %s batch timed out; retrying once", provider))
		result, retryErr := run()
		if retryErr == nil {
			return result, nil
		}
		return result, &batchAttemptError{err: retryErr, attempts: 2}
	}
	return result, err
}

// recordBatchFailure records a batch embedding failure and may disable batch mode.
// Caller must hold m.mu (called from embedChunks inside sync).
func (m *MemorySearchManager) recordBatchFailure(provider string, err error, attempts int, forceDisable bool) (bool, int) {
	if m == nil {
		return true, 0
	}
	increment := attempts
	if increment < 1 {
		increment = 1
	}
	if forceDisable {
		increment = batchFailureLimit
	}
	m.batchFailures += increment
	if err != nil {
		m.batchLastError = err.Error()
	}
	m.batchLastProvider = provider
	disabled := forceDisable || m.batchFailures >= batchFailureLimit
	if disabled {
		m.batchEnabled = false
	}
	return disabled, m.batchFailures
}

func batchCustomID(source, relPath string, chunkHash string, startLine, endLine, index int) string {
	payload := fmt.Sprintf("%s:%s:%d:%d:%s:%d", source, relPath, startLine, endLine, chunkHash, index)
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

func runWithConcurrency[T any](tasks []func() (T, error), limit int) (map[int]T, error) {
	if len(tasks) == 0 {
		return map[int]T{}, nil
	}
	if limit <= 0 {
		limit = 1
	}
	if limit > len(tasks) {
		limit = len(tasks)
	}
	results := make(map[int]T, len(tasks))
	errCh := make(chan error, 1)
	var next int
	var mu sync.Mutex
	wg := sync.WaitGroup{}
	worker := func() {
		defer wg.Done()
		for {
			mu.Lock()
			index := next
			next++
			mu.Unlock()
			if index >= len(tasks) {
				return
			}
			res, err := tasks[index]()
			if err != nil {
				select {
				case errCh <- err:
				default:
				}
				return
			}
			mu.Lock()
			results[index] = res
			mu.Unlock()
		}
	}
	wg.Add(limit)
	for range limit {
		go worker()
	}
	wg.Wait()
	select {
	case err := <-errCh:
		return nil, err
	default:
	}
	return results, nil
}

func openAIHeaders(apiKey string, headers map[string]string) http.Header {
	out := http.Header{}
	for key, value := range headers {
		if strings.TrimSpace(value) == "" {
			continue
		}
		out.Set(key, value)
	}
	if apiKey != "" {
		hasAuth := false
		for key := range out {
			if strings.EqualFold(key, "Authorization") {
				hasAuth = true
				break
			}
		}
		if !hasAuth {
			out.Set("Authorization", "Bearer "+apiKey)
		}
	}
	if out.Get("Content-Type") == "" {
		out.Set("Content-Type", "application/json")
	}
	return out
}

func geminiHeaders(apiKey string, headers map[string]string) http.Header {
	out := http.Header{}
	for key, value := range headers {
		if strings.TrimSpace(value) == "" {
			continue
		}
		out.Set(key, value)
	}
	if apiKey != "" {
		hasKey := false
		for key := range out {
			if strings.EqualFold(key, "x-goog-api-key") {
				hasKey = true
				break
			}
		}
		if !hasKey {
			out.Set("x-goog-api-key", apiKey)
		}
	}
	if out.Get("Content-Type") == "" {
		out.Set("Content-Type", "application/json")
	}
	return out
}

func normalizeBaseURL(raw string) string {
	return stringutil.NormalizeBaseURL(raw)
}

func buildOpenAIRequests(relPath, source, model string, missing []missingChunk) ([]openAIBatchRequest, map[string]missingChunk) {
	requests := make([]openAIBatchRequest, 0, len(missing))
	mapping := make(map[string]missingChunk, len(missing))
	for _, item := range missing {
		customID := batchCustomID(source, relPath, item.chunk.Hash, item.chunk.StartLine, item.chunk.EndLine, item.index)
		mapping[customID] = item
		req := openAIBatchRequest{
			CustomID: customID,
			Method:   http.MethodPost,
			URL:      "/v1/embeddings",
		}
		req.Body.Model = model
		req.Body.Input = item.chunk.Text
		requests = append(requests, req)
	}
	return requests, mapping
}

func buildGeminiRequests(relPath, source string, missing []missingChunk) ([]geminiBatchRequest, map[string]missingChunk) {
	requests := make([]geminiBatchRequest, 0, len(missing))
	mapping := make(map[string]missingChunk, len(missing))
	for _, item := range missing {
		customID := batchCustomID(source, relPath, item.chunk.Hash, item.chunk.StartLine, item.chunk.EndLine, item.index)
		mapping[customID] = item
		req := geminiBatchRequest{
			CustomID: customID,
			TaskType: "RETRIEVAL_DOCUMENT",
		}
		req.Content.Parts = []struct {
			Text string `json:"text"`
		}{{Text: item.chunk.Text}}
		requests = append(requests, req)
	}
	return requests, mapping
}

// batchResult represents a single embedding result from a batch output line.
type batchResult struct {
	CustomID  string
	Embedding []float64
	Error     string
}

// batchRunner holds provider-specific callbacks for the generic runBatches function.
type batchRunner[R any] struct {
	Label       string
	Client      *http.Client
	Concurrency int
	Split       func(requests []R) [][]R
	CustomIDs   func(group []R) []string
	Submit      func(ctx context.Context, client *http.Client, group []R) (batchID, outputFileID string, err error)
	Fetch       func(ctx context.Context, client *http.Client, outputFileID string) (content string, err error)
	Parse       func(content string) []batchResult
}

// runBatches splits requests into groups, submits each as a batch, fetches
// results, and collects embeddings. Provider-specific logic is injected via
// the batchRunner callbacks.
func runBatches[R any](ctx context.Context, requests []R, runner batchRunner[R]) (map[string][]float64, error) {
	if len(requests) == 0 {
		return map[string][]float64{}, nil
	}
	groups := runner.Split(requests)
	client := runner.Client
	if client == nil {
		client = http.DefaultClient
	}
	byCustomID := make(map[string][]float64)
	tasks := make([]func() (struct{}, error), 0, len(groups))
	for _, group := range groups {
		tasks = append(tasks, func() (struct{}, error) {
			batchID, outputFileID, err := runner.Submit(ctx, client, group)
			if err != nil {
				return struct{}{}, err
			}
			if outputFileID == "" {
				return struct{}{}, fmt.Errorf("%s batch %s completed without output file", runner.Label, batchID)
			}
			content, err := runner.Fetch(ctx, client, outputFileID)
			if err != nil {
				return struct{}{}, err
			}
			results := runner.Parse(content)
			remaining := make(map[string]struct{})
			for _, id := range runner.CustomIDs(group) {
				remaining[id] = struct{}{}
			}
			var errs []string
			for _, r := range results {
				if r.CustomID == "" {
					continue
				}
				delete(remaining, r.CustomID)
				if r.Error != "" {
					errs = append(errs, fmt.Sprintf("%s: %s", r.CustomID, r.Error))
					continue
				}
				if len(r.Embedding) == 0 {
					errs = append(errs, fmt.Sprintf("%s: empty embedding", r.CustomID))
					continue
				}
				byCustomID[r.CustomID] = r.Embedding
			}
			if len(errs) > 0 {
				return struct{}{}, fmt.Errorf("%s batch %s failed: %s", runner.Label, batchID, strings.Join(errs, "; "))
			}
			if len(remaining) > 0 {
				return struct{}{}, fmt.Errorf("%s batch %s missing %d embedding responses", runner.Label, batchID, len(remaining))
			}
			return struct{}{}, nil
		})
	}
	_, err := runWithConcurrency(tasks, runner.Concurrency)
	if err != nil {
		return nil, err
	}
	return byCustomID, nil
}

func openAICustomIDs(group []openAIBatchRequest) []string {
	ids := make([]string, len(group))
	for i, req := range group {
		ids[i] = req.CustomID
	}
	return ids
}

func parseOpenAIResults(content string) []batchResult {
	lines := parseOpenAIBatchOutput(content)
	results := make([]batchResult, 0, len(lines))
	for _, line := range lines {
		r := batchResult{CustomID: line.CustomID}
		switch {
		case line.Error.Message != "":
			r.Error = line.Error.Message
		case line.Response.StatusCode >= 400:
			r.Error = line.Response.Body.Error.Message
			if r.Error == "" {
				r.Error = fmt.Sprintf("status %d", line.Response.StatusCode)
			}
		case len(line.Response.Body.Data) > 0 && len(line.Response.Body.Data[0].Embedding) > 0:
			r.Embedding = line.Response.Body.Data[0].Embedding
		}
		results = append(results, r)
	}
	return results
}

func runOpenAIBatches(ctx context.Context, params openAIBatchParams) (map[string][]float64, error) {
	return runBatches(ctx, params.Requests, batchRunner[openAIBatchRequest]{
		Label:       "openai",
		Client:      params.Client,
		Concurrency: params.Concurrency,
		Split:       splitOpenAIRequests,
		CustomIDs:   openAICustomIDs,
		Submit: func(ctx context.Context, client *http.Client, group []openAIBatchRequest) (string, string, error) {
			return submitOpenAIBatch(ctx, client, params, group)
		},
		Fetch: func(ctx context.Context, client *http.Client, outputFileID string) (string, error) {
			return fetchOpenAIFile(ctx, client, params, outputFileID)
		},
		Parse: parseOpenAIResults,
	})
}

type openAIBatchParams struct {
	BaseURL      string
	APIKey       string
	Headers      map[string]string
	AgentID      string
	Requests     []openAIBatchRequest
	Wait         bool
	PollInterval time.Duration
	Timeout      time.Duration
	Concurrency  int
	Client       *http.Client
}

func splitOpenAIRequests(requests []openAIBatchRequest) [][]openAIBatchRequest {
	return slices.Collect(slices.Chunk(requests, openAIBatchMaxRequests))
}

func submitOpenAIBatch(
	ctx context.Context,
	client *http.Client,
	params openAIBatchParams,
	requests []openAIBatchRequest,
) (string, string, error) {
	baseURL := normalizeBaseURL(params.BaseURL)
	jsonl := make([]string, 0, len(requests))
	for _, req := range requests {
		raw, _ := json.Marshal(req)
		jsonl = append(jsonl, string(raw))
	}
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	if err := writer.WriteField("purpose", "batch"); err != nil {
		return "", "", err
	}
	part, err := writer.CreateFormFile("file", "memory-embeddings.jsonl")
	if err != nil {
		return "", "", err
	}
	if _, err := io.WriteString(part, strings.Join(jsonl, "\n")); err != nil {
		return "", "", err
	}
	if err := writer.Close(); err != nil {
		return "", "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/files", body)
	if err != nil {
		return "", "", err
	}
	for key, value := range openAIHeaders(params.APIKey, params.Headers) {
		req.Header[key] = value
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("openai batch file upload failed: %s %s", resp.Status, string(payload))
	}
	var fileResp struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(payload, &fileResp); err != nil {
		return "", "", err
	}
	if fileResp.ID == "" {
		return "", "", errors.New("openai batch file upload failed: missing file id")
	}

	batchBody := map[string]any{
		"input_file_id":     fileResp.ID,
		"endpoint":          "/v1/embeddings",
		"completion_window": openAIBatchCompletionWindow,
		"metadata": map[string]any{
			"source": "ai-bridge-memory",
			"agent":  params.AgentID,
		},
	}
	batchJSON, _ := json.Marshal(batchBody)
	batchReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/batches", bytes.NewReader(batchJSON))
	if err != nil {
		return "", "", err
	}
	for key, value := range openAIHeaders(params.APIKey, params.Headers) {
		batchReq.Header[key] = value
	}
	resp, err = client.Do(batchReq)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	payload, _ = io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("openai batch create failed: %s %s", resp.Status, string(payload))
	}
	var batchStatus openAIBatchStatus
	if err := json.Unmarshal(payload, &batchStatus); err != nil {
		return "", "", err
	}
	if batchStatus.ID == "" {
		return "", "", errors.New("openai batch create failed: missing batch id")
	}
	if batchStatus.Status == "completed" {
		return batchStatus.ID, batchStatus.OutputFile, nil
	}
	if !params.Wait {
		return "", "", fmt.Errorf("openai batch %s still %s; wait disabled", batchStatus.ID, batchStatus.Status)
	}
	outputID, err := waitForOpenAIBatch(ctx, client, params, batchStatus.ID)
	return batchStatus.ID, outputID, err
}

func waitForOpenAIBatch(ctx context.Context, client *http.Client, params openAIBatchParams, batchID string) (string, error) {
	deadline := time.Now().Add(params.Timeout)
	for {
		status, err := fetchOpenAIBatchStatus(ctx, client, params, batchID)
		if err != nil {
			return "", err
		}
		switch status.Status {
		case "completed":
			if status.OutputFile == "" {
				return "", fmt.Errorf("openai batch %s completed without output file", batchID)
			}
			return status.OutputFile, nil
		case "failed", "expired", "cancelled", "canceled":
			return "", fmt.Errorf("openai batch %s %s", batchID, status.Status)
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("openai batch %s timed out", batchID)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(params.PollInterval):
		}
	}
}

func fetchOpenAIBatchStatus(ctx context.Context, client *http.Client, params openAIBatchParams, batchID string) (*openAIBatchStatus, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, normalizeBaseURL(params.BaseURL)+"/batches/"+batchID, nil)
	if err != nil {
		return nil, err
	}
	for key, value := range openAIHeaders(params.APIKey, params.Headers) {
		req.Header[key] = value
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("openai batch status failed: %s %s", resp.Status, string(payload))
	}
	var status openAIBatchStatus
	if err := json.Unmarshal(payload, &status); err != nil {
		return nil, err
	}
	return &status, nil
}

func fetchOpenAIFile(ctx context.Context, client *http.Client, params openAIBatchParams, fileID string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, normalizeBaseURL(params.BaseURL)+"/files/"+fileID+"/content", nil)
	if err != nil {
		return "", err
	}
	for key, value := range openAIHeaders(params.APIKey, params.Headers) {
		req.Header[key] = value
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("openai batch file content failed: %s %s", resp.Status, string(payload))
	}
	return string(payload), nil
}

func parseOpenAIBatchOutput(text string) []openAIBatchOutputLine {
	lines := strings.Split(text, "\n")
	var out []openAIBatchOutputLine
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry openAIBatchOutputLine
		if err := json.Unmarshal([]byte(line), &entry); err == nil {
			out = append(out, entry)
		}
	}
	return out
}

type geminiBatchParams struct {
	BaseURL      string
	APIKey       string
	Headers      map[string]string
	AgentID      string
	Model        string
	Requests     []geminiBatchRequest
	Wait         bool
	PollInterval time.Duration
	Timeout      time.Duration
	Concurrency  int
	Client       *http.Client
}

func geminiCustomIDs(group []geminiBatchRequest) []string {
	ids := make([]string, len(group))
	for i, req := range group {
		ids[i] = req.CustomID
	}
	return ids
}

func parseGeminiResults(content string) []batchResult {
	lines := parseGeminiBatchOutput(content)
	results := make([]batchResult, 0, len(lines))
	for _, line := range lines {
		custom := stringutil.FirstNonEmpty(line.Key, line.CustomID, line.RequestID)
		r := batchResult{CustomID: custom}
		switch {
		case line.Error.Message != "":
			r.Error = line.Error.Message
		case line.Response.Error.Message != "":
			r.Error = line.Response.Error.Message
		default:
			r.Embedding = line.Embedding.Values
			if len(r.Embedding) == 0 {
				r.Embedding = line.Response.Embedding.Values
			}
		}
		results = append(results, r)
	}
	return results
}

func runGeminiBatches(ctx context.Context, params geminiBatchParams) (map[string][]float64, error) {
	return runBatches(ctx, params.Requests, batchRunner[geminiBatchRequest]{
		Label:       "gemini",
		Client:      params.Client,
		Concurrency: params.Concurrency,
		Split:       splitGeminiRequests,
		CustomIDs:   geminiCustomIDs,
		Submit: func(ctx context.Context, client *http.Client, group []geminiBatchRequest) (string, string, error) {
			return submitGeminiBatch(ctx, client, params, group)
		},
		Fetch: func(ctx context.Context, client *http.Client, outputFileID string) (string, error) {
			return fetchGeminiFile(ctx, client, params, outputFileID)
		},
		Parse: parseGeminiResults,
	})
}

func splitGeminiRequests(requests []geminiBatchRequest) [][]geminiBatchRequest {
	return slices.Collect(slices.Chunk(requests, geminiBatchMaxRequests))
}

func submitGeminiBatch(
	ctx context.Context,
	client *http.Client,
	params geminiBatchParams,
	requests []geminiBatchRequest,
) (string, string, error) {
	baseURL := normalizeBaseURL(params.BaseURL)
	jsonlLines := make([]string, 0, len(requests))
	for _, req := range requests {
		line, _ := json.Marshal(map[string]any{
			"key": req.CustomID,
			"request": map[string]any{
				"content":   req.Content,
				"task_type": req.TaskType,
			},
		})
		jsonlLines = append(jsonlLines, string(line))
	}
	jsonl := strings.Join(jsonlLines, "\n")
	displayName := fmt.Sprintf("memory-embeddings-%d", time.Now().UnixNano())
	uploadBody, contentType := buildGeminiUploadBody(displayName, jsonl)

	uploadURL := geminiUploadURL(baseURL) + "/files?uploadType=multipart"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, bytes.NewReader(uploadBody))
	if err != nil {
		return "", "", err
	}
	for key, value := range geminiHeaders(params.APIKey, params.Headers) {
		req.Header[key] = value
	}
	req.Header.Set("Content-Type", contentType)

	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("gemini batch file upload failed: %s %s", resp.Status, string(payload))
	}
	var fileResp struct {
		Name string `json:"name"`
		File struct {
			Name string `json:"name"`
		} `json:"file"`
	}
	if err := json.Unmarshal(payload, &fileResp); err != nil {
		return "", "", err
	}
	fileID := fileResp.Name
	if fileID == "" {
		fileID = fileResp.File.Name
	}
	if fileID == "" {
		return "", "", errors.New("gemini batch file upload failed: missing file id")
	}

	modelPath := geminiModelPath(params.Model)
	batchURL := fmt.Sprintf("%s/%s:asyncBatchEmbedContent", baseURL, modelPath)
	batchBody := map[string]any{
		"batch": map[string]any{
			"displayName": fmt.Sprintf("memory-embeddings-%s", params.AgentID),
			"inputConfig": map[string]any{
				"file_name": fileID,
			},
		},
	}
	batchJSON, _ := json.Marshal(batchBody)
	batchReq, err := http.NewRequestWithContext(ctx, http.MethodPost, batchURL, bytes.NewReader(batchJSON))
	if err != nil {
		return "", "", err
	}
	for key, value := range geminiHeaders(params.APIKey, params.Headers) {
		batchReq.Header[key] = value
	}
	resp, err = client.Do(batchReq)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	payload, _ = io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.StatusCode == 404 {
			return "", "", errors.New("gemini batch create failed: 404 (asyncBatchEmbedContent not available)")
		}
		return "", "", fmt.Errorf("gemini batch create failed: %s %s", resp.Status, string(payload))
	}
	var status geminiBatchStatus
	if err := json.Unmarshal(payload, &status); err != nil {
		return "", "", err
	}
	if status.Name == "" {
		return "", "", errors.New("gemini batch create failed: missing batch name")
	}
	if isGeminiBatchComplete(status.State) {
		output := resolveGeminiOutput(&status)
		return status.Name, output, nil
	}
	if !params.Wait {
		return "", "", fmt.Errorf("gemini batch %s still %s; wait disabled", status.Name, status.State)
	}
	outputID, err := waitForGeminiBatch(ctx, client, params, status.Name)
	return status.Name, outputID, err
}

func geminiUploadURL(baseURL string) string {
	if strings.Contains(baseURL, "/v1beta") {
		return strings.TrimRight(strings.Replace(baseURL, "/v1beta", "/upload/v1beta", 1), "/")
	}
	return baseURL + "/upload"
}

func buildGeminiUploadBody(displayName, jsonl string) ([]byte, string) {
	boundary := fmt.Sprintf("ai-bridge-%x", sha256.Sum256([]byte(displayName)))
	delimiter := fmt.Sprintf("--%s\r\n", boundary)
	closeDelimiter := fmt.Sprintf("--%s--\r\n", boundary)
	meta := fmt.Sprintf(`{"file":{"displayName":"%s","mimeType":"application/jsonl"}}`, displayName)
	var buf bytes.Buffer
	buf.WriteString(delimiter)
	buf.WriteString("Content-Type: application/json; charset=UTF-8\r\n\r\n")
	buf.WriteString(meta)
	buf.WriteString("\r\n")
	buf.WriteString(delimiter)
	buf.WriteString("Content-Type: application/jsonl; charset=UTF-8\r\n\r\n")
	buf.WriteString(jsonl)
	buf.WriteString("\r\n")
	buf.WriteString(closeDelimiter)
	return buf.Bytes(), fmt.Sprintf("multipart/related; boundary=%s", boundary)
}

func fetchGeminiFile(ctx context.Context, client *http.Client, params geminiBatchParams, fileID string) (string, error) {
	baseURL := normalizeBaseURL(params.BaseURL)
	if !strings.HasPrefix(fileID, "files/") {
		fileID = "files/" + fileID
	}
	url := fmt.Sprintf("%s/%s:download", baseURL, fileID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	for key, value := range geminiHeaders(params.APIKey, params.Headers) {
		req.Header[key] = value
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("gemini batch file content failed: %s %s", resp.Status, string(payload))
	}
	return string(payload), nil
}

func waitForGeminiBatch(ctx context.Context, client *http.Client, params geminiBatchParams, batchName string) (string, error) {
	deadline := time.Now().Add(params.Timeout)
	for {
		status, err := fetchGeminiBatchStatus(ctx, client, params, batchName)
		if err != nil {
			return "", err
		}
		if isGeminiBatchComplete(status.State) {
			output := resolveGeminiOutput(status)
			if output == "" {
				return "", fmt.Errorf("gemini batch %s completed without output file", batchName)
			}
			return output, nil
		}
		if isGeminiBatchFailed(status.State) {
			msg := status.Error.Message
			if msg == "" {
				msg = "unknown error"
			}
			return "", fmt.Errorf("gemini batch %s %s: %s", batchName, status.State, msg)
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("gemini batch %s timed out", batchName)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(params.PollInterval):
		}
	}
}

func fetchGeminiBatchStatus(ctx context.Context, client *http.Client, params geminiBatchParams, batchName string) (*geminiBatchStatus, error) {
	baseURL := normalizeBaseURL(params.BaseURL)
	name := batchName
	if !strings.HasPrefix(name, "batches/") {
		name = path.Join("batches", name)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/"+name, nil)
	if err != nil {
		return nil, err
	}
	for key, value := range geminiHeaders(params.APIKey, params.Headers) {
		req.Header[key] = value
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("gemini batch status failed: %s %s", resp.Status, string(payload))
	}
	var status geminiBatchStatus
	if err := json.Unmarshal(payload, &status); err != nil {
		return nil, err
	}
	return &status, nil
}

func parseGeminiBatchOutput(text string) []geminiBatchOutputLine {
	lines := strings.Split(text, "\n")
	var out []geminiBatchOutputLine
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry geminiBatchOutputLine
		if err := json.Unmarshal([]byte(line), &entry); err == nil {
			out = append(out, entry)
		}
	}
	return out
}

func resolveGeminiOutput(status *geminiBatchStatus) string {
	if status == nil {
		return ""
	}
	if status.OutputConfig.File != "" {
		return status.OutputConfig.File
	}
	if status.OutputConfig.FileID != "" {
		return status.OutputConfig.FileID
	}
	return status.Metadata.Output.ResponsesFile
}

func isGeminiBatchComplete(state string) bool {
	switch strings.ToUpper(state) {
	case "SUCCEEDED", "COMPLETED", "DONE":
		return true
	default:
		return false
	}
}

func isGeminiBatchFailed(state string) bool {
	switch strings.ToUpper(state) {
	case "FAILED", "CANCELLED", "CANCELED", "EXPIRED":
		return true
	default:
		return false
	}
}

func geminiModelPath(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}
	if strings.HasPrefix(model, "models/") {
		return model
	}
	return "models/" + model
}
