package exa

import "testing"

func TestShouldAttachBearerAuth(t *testing.T) {
	t.Helper()

	tests := []struct {
		name    string
		baseURL string
		want    bool
	}{
		{name: "default exa endpoint", baseURL: "https://api.exa.ai", want: false},
		{name: "default exa endpoint with path", baseURL: "https://api.exa.ai/contents", want: false},
		{name: "default exa search path", baseURL: "https://api.exa.ai/search", want: false},
		{name: "proxy endpoint", baseURL: "https://ai.bt.hn/exa", want: true},
		{name: "relative path", baseURL: "/exa", want: true},
		{name: "empty", baseURL: "", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ShouldAttachBearerAuth(tc.baseURL)
			if got != tc.want {
				t.Fatalf("ShouldAttachBearerAuth(%q) = %v, want %v", tc.baseURL, got, tc.want)
			}
		})
	}
}
