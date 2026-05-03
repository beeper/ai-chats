package connector

import "testing"

func TestModelMatchesQuery(t *testing.T) {
	model := &ModelInfo{
		ID:       "openrouter/openai/gpt-4.1",
		Name:     "GPT-4.1",
		Provider: "openrouter",
	}

	tests := []struct {
		name  string
		query string
		want  bool
	}{
		{name: "match id", query: "gpt-4.1", want: true},
		{name: "match display name", query: "gpt 4.1", want: true},
		{name: "match provider alias", query: "openrouter/gpt 4.1", want: true},
		{name: "match openrouter url", query: "openrouter.ai/models/openai/gpt-4.1", want: true},
		{name: "non-match", query: "claude", want: false},
		{name: "empty query", query: "", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := modelMatchesQuery(tc.query, model)
			if got != tc.want {
				t.Fatalf("modelMatchesQuery(%q) = %v, want %v", tc.query, got, tc.want)
			}
		})
	}
}
