package embedding

import (
	"context"
	"fmt"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"github.com/beeper/ai-bridge/pkg/shared/httputil"
)

const (
	DefaultOpenAIBaseURL        = "https://api.openai.com/v1"
	DefaultOpenAIEmbeddingModel = "text-embedding-3-small"
)

func NormalizeOpenAIModel(model string) string {
	trimmed := strings.TrimSpace(model)
	if trimmed == "" {
		return DefaultOpenAIEmbeddingModel
	}
	if after, ok := strings.CutPrefix(trimmed, "openai/"); ok {
		return after
	}
	return trimmed
}

func NewOpenAIProvider(apiKey, baseURL, model string, headers map[string]string) (*Provider, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("openai embeddings require api_key")
	}
	if strings.TrimSpace(baseURL) == "" {
		baseURL = DefaultOpenAIBaseURL
	}
	opts := []option.RequestOption{option.WithAPIKey(apiKey)}
	opts = httputil.AppendHeaderOptions(opts, headers)
	opts = append(opts, option.WithBaseURL(baseURL))
	client := openai.NewClient(opts...)
	normalized := NormalizeOpenAIModel(model)

	embedBatch := func(ctx context.Context, texts []string) ([][]float64, error) {
		if len(texts) == 0 {
			return nil, nil
		}
		params := openai.EmbeddingNewParams{
			Model: openai.EmbeddingModel(normalized),
			Input: openai.EmbeddingNewParamsInputUnion{
				OfArrayOfStrings: texts,
			},
			EncodingFormat: openai.EmbeddingNewParamsEncodingFormatFloat,
		}
		resp, err := client.Embeddings.New(ctx, params)
		if err != nil {
			return nil, err
		}
		out := make([][]float64, 0, len(resp.Data))
		for _, entry := range resp.Data {
			out = append(out, NormalizeEmbedding(entry.Embedding))
		}
		return out, nil
	}

	return &Provider{
		id:    "openai",
		model: normalized,
		embedQuery: func(ctx context.Context, text string) ([]float64, error) {
			results, err := embedBatch(ctx, []string{text})
			if err != nil {
				return nil, err
			}
			if len(results) == 0 {
				return nil, nil
			}
			return results[0], nil
		},
		embedBatch: embedBatch,
	}, nil
}
