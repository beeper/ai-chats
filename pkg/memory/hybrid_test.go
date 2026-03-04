package memory

import (
	"math"
	"testing"
)

func TestBM25RankToScore(t *testing.T) {
	if score := BM25RankToScore(-1); score != 1 {
		t.Fatalf("expected negative rank to clamp to 1, got %v", score)
	}
	if score := BM25RankToScore(0); score != 1 {
		t.Fatalf("expected rank 0 to be 1, got %v", score)
	}
	if score := BM25RankToScore(1); score <= 0.4 || score >= 0.6 {
		t.Fatalf("expected rank 1 to be around 0.5, got %v", score)
	}
}

func TestBM25RankToScore_NonFinite(t *testing.T) {
	nanScore := BM25RankToScore(math.NaN())
	infScore := BM25RankToScore(math.Inf(1))
	expected := 1.0 / (1 + 999)
	if nanScore != expected {
		t.Fatalf("expected NaN rank to return %v, got %v", expected, nanScore)
	}
	if infScore != expected {
		t.Fatalf("expected Inf rank to return %v, got %v", expected, infScore)
	}
}

func TestBuildFtsQuery(t *testing.T) {
	if got := BuildFtsQuery(""); got != "" {
		t.Fatalf("expected empty query for empty input, got %q", got)
	}
	if got := BuildFtsQuery("hello world"); got != `"hello" AND "world"` {
		t.Fatalf("unexpected FTS query: %q", got)
	}
	if got := BuildFtsQuery("  !!!  "); got != "" {
		t.Fatalf("expected empty query for punctuation-only input, got %q", got)
	}
}
