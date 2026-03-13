package memory

import (
	"math"
	"regexp"
	"strings"
)

var tokenRE = regexp.MustCompile(`[A-Za-z0-9_]+`)

// BuildFtsQuery builds a simple AND query for FTS5 from raw input.
func BuildFtsQuery(raw string) string {
	tokens := tokenRE.FindAllString(raw, -1)
	if len(tokens) == 0 {
		return ""
	}
	parts := make([]string, len(tokens))
	for i, token := range tokens {
		parts[i] = `"` + token + `"`
	}
	return strings.Join(parts, " AND ")
}

// BM25RankToScore normalizes an FTS5 bm25 rank into a 0-1-ish score.
func BM25RankToScore(rank float64) float64 {
	if math.IsNaN(rank) || math.IsInf(rank, 0) {
		rank = 999
	}
	if rank < 0 {
		rank = 0
	}
	return 1 / (1 + rank)
}

// HybridKeywordResult holds a single keyword/FTS search result with a text relevance score.
type HybridKeywordResult struct {
	ID        string
	Path      string
	StartLine int
	EndLine   int
	Source    string
	Snippet   string
	TextScore float64
}
