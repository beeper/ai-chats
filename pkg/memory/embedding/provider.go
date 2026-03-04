package embedding

import (
	"context"
	"errors"
	"math"
)

// Provider wraps embed functions for a specific embedding backend (e.g. OpenAI, Gemini).
type Provider struct {
	id         string
	model      string
	embedQuery func(ctx context.Context, text string) ([]float64, error)
	embedBatch func(ctx context.Context, texts []string) ([][]float64, error)
}

func (p *Provider) ID() string    { return p.id }
func (p *Provider) Model() string { return p.model }

func (p *Provider) EmbedQuery(ctx context.Context, text string) ([]float64, error) {
	if p.embedQuery == nil {
		return nil, errors.New("embedding provider: query not configured")
	}
	return p.embedQuery(ctx, text)
}

func (p *Provider) EmbedBatch(ctx context.Context, texts []string) ([][]float64, error) {
	if p.embedBatch == nil {
		return nil, errors.New("embedding provider: batch not configured")
	}
	return p.embedBatch(ctx, texts)
}

// NormalizeEmbedding L2-normalizes a vector in-place, replacing NaN/Inf with 0.
// Returns the input unchanged if the vector is empty or has near-zero magnitude.
func NormalizeEmbedding(vec []float64) []float64 {
	if len(vec) == 0 {
		return vec
	}
	var sumSq float64
	for _, v := range vec {
		if !math.IsNaN(v) && !math.IsInf(v, 0) {
			sumSq += v * v
		}
	}
	mag := math.Sqrt(sumSq)
	if mag < 1e-10 {
		return vec
	}
	out := make([]float64, len(vec))
	for i, v := range vec {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			out[i] = 0
		} else {
			out[i] = v / mag
		}
	}
	return out
}
