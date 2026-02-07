package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"strings"
)

const (
	DefaultGeminiBaseURL        = "https://generativelanguage.googleapis.com/v1beta"
	DefaultGeminiEmbeddingModel = "gemini-embedding-001"
)

type geminiClient struct {
	baseURL   string
	headers   map[string]string
	model     string
	modelPath string
}

func NormalizeGeminiModel(model string) string {
	trimmed := strings.TrimSpace(model)
	if trimmed == "" {
		return DefaultGeminiEmbeddingModel
	}
	withoutPrefix := strings.TrimPrefix(trimmed, "models/")
	if after, ok := strings.CutPrefix(withoutPrefix, "gemini/"); ok {
		return after
	}
	if after, ok := strings.CutPrefix(withoutPrefix, "google/"); ok {
		return after
	}
	return withoutPrefix
}

func normalizeGeminiBaseURL(raw string) string {
	trimmed := strings.TrimRight(raw, "/")
	if idx := strings.Index(trimmed, "/openai"); idx > -1 {
		return trimmed[:idx]
	}
	return trimmed
}

func buildGeminiModelPath(model string) string {
	if strings.HasPrefix(model, "models/") {
		return model
	}
	return "models/" + model
}

func NewGeminiProvider(apiKey, baseURL, model string, headers map[string]string) (*Provider, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("gemini embeddings require api_key")
	}
	if strings.TrimSpace(baseURL) == "" {
		baseURL = DefaultGeminiBaseURL
	}
	normalized := NormalizeGeminiModel(model)
	client := &geminiClient{
		baseURL:   normalizeGeminiBaseURL(baseURL),
		headers:   mergeHeaders(map[string]string{"x-goog-api-key": apiKey}, headers),
		model:     normalized,
		modelPath: buildGeminiModelPath(normalized),
	}

	embedQuery := func(ctx context.Context, text string) ([]float64, error) {
		if strings.TrimSpace(text) == "" {
			return nil, nil
		}
		body := map[string]any{
			"content": map[string]any{
				"parts": []map[string]any{{"text": text}},
			},
			"taskType": "RETRIEVAL_QUERY",
		}
		resp, err := client.post(ctx, client.embedURL(), body)
		if err != nil {
			return nil, err
		}
		var payload struct {
			Embedding struct {
				Values []float64 `json:"values"`
			} `json:"embedding"`
		}
		if err := json.Unmarshal(resp, &payload); err != nil {
			return nil, err
		}
		return NormalizeEmbedding(payload.Embedding.Values), nil
	}

	embedBatch := func(ctx context.Context, texts []string) ([][]float64, error) {
		if len(texts) == 0 {
			return nil, nil
		}
		requests := make([]map[string]any, 0, len(texts))
		for _, text := range texts {
			requests = append(requests, map[string]any{
				"model": client.modelPath,
				"content": map[string]any{
					"parts": []map[string]any{{"text": text}},
				},
				"taskType": "RETRIEVAL_DOCUMENT",
			})
		}
		body := map[string]any{"requests": requests}
		resp, err := client.post(ctx, client.batchURL(), body)
		if err != nil {
			return nil, err
		}
		var payload struct {
			Embeddings []struct {
				Values []float64 `json:"values"`
			} `json:"embeddings"`
		}
		if err := json.Unmarshal(resp, &payload); err != nil {
			return nil, err
		}
		results := make([][]float64, 0, len(texts))
		for i := range texts {
			if i < len(payload.Embeddings) {
				results = append(results, NormalizeEmbedding(payload.Embeddings[i].Values))
			} else {
				results = append(results, nil)
			}
		}
		return results, nil
	}

	return &Provider{
		id:         "gemini",
		model:      normalized,
		embedQuery: embedQuery,
		embedBatch: embedBatch,
	}, nil
}

func (c *geminiClient) embedURL() string {
	return strings.TrimRight(c.baseURL, "/") + "/" + c.modelPath + ":embedContent"
}

func (c *geminiClient) batchURL() string {
	return strings.TrimRight(c.baseURL, "/") + "/" + c.modelPath + ":batchEmbedContents"
}

func (c *geminiClient) post(ctx context.Context, url string, payload map[string]any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for key, value := range c.headers {
		if strings.TrimSpace(value) == "" {
			continue
		}
		req.Header.Set(key, value)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("gemini embeddings failed: %s %s", resp.Status, string(data))
	}
	return data, nil
}

func mergeHeaders(base map[string]string, overrides map[string]string) map[string]string {
	out := maps.Clone(base)
	if out == nil {
		out = make(map[string]string)
	}
	maps.Copy(out, overrides)
	return out
}
