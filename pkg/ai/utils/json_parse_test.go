package utils

import "testing"

func TestParseStreamingJSON(t *testing.T) {
	parsed := ParseStreamingJSON(`{"a":1,"b":"x"}`)
	if parsed["a"] != float64(1) || parsed["b"] != "x" {
		t.Fatalf("unexpected parsed output: %#v", parsed)
	}

	partial := ParseStreamingJSON(`{"a":1,"b":"x`)
	if len(partial) != 0 {
		t.Fatalf("expected empty fallback map for malformed partial json, got %#v", partial)
	}
}
