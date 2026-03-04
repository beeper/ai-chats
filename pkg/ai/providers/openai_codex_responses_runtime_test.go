package providers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/beeper/ai-bridge/pkg/ai"
)

func TestStreamOpenAICodexResponses_MissingAPIKeyEmitsError(t *testing.T) {
	stream := streamOpenAICodexResponses(ai.Model{
		ID:       "gpt-5.1-codex-mini",
		Provider: "openai-codex",
		API:      ai.APIOpenAICodexResponse,
	}, ai.Context{
		Messages: []ai.Message{{Role: ai.RoleUser, Text: "hello"}},
	}, &ai.StreamOptions{})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	evt, err := stream.Next(ctx)
	if err != nil {
		t.Fatalf("expected terminal error event, got %v", err)
	}
	if evt.Type != ai.EventError {
		t.Fatalf("expected error event, got %s", evt.Type)
	}
	if !strings.Contains(strings.ToLower(evt.Error.ErrorMessage), "api key") {
		t.Fatalf("expected missing api key message, got %q", evt.Error.ErrorMessage)
	}
	if _, err := stream.Next(ctx); err != io.EOF {
		t.Fatalf("expected EOF after terminal event, got %v", err)
	}
}

func TestResolveCodexSDKBaseURL(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{in: "", want: "https://chatgpt.com/backend-api/codex"},
		{in: "https://chatgpt.com/backend-api", want: "https://chatgpt.com/backend-api/codex"},
		{in: "https://chatgpt.com/backend-api/codex", want: "https://chatgpt.com/backend-api/codex"},
		{in: "https://chatgpt.com/backend-api/codex/responses", want: "https://chatgpt.com/backend-api/codex"},
	}
	for _, tc := range cases {
		if got := resolveCodexSDKBaseURL(tc.in); got != tc.want {
			t.Fatalf("resolveCodexSDKBaseURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestExtractCodexAccountID(t *testing.T) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payloadObj := map[string]any{
		codexJWTClaimPath: map[string]any{
			"chatgpt_account_id": "acct_123",
		},
	}
	payloadBytes, _ := json.Marshal(payloadObj)
	payload := base64.RawURLEncoding.EncodeToString(payloadBytes)
	token := header + "." + payload + ".sig"
	if got := extractCodexAccountID(token); got != "acct_123" {
		t.Fatalf("expected account id acct_123, got %q", got)
	}
	if got := extractCodexAccountID("not-a-jwt"); got != "" {
		t.Fatalf("expected empty account id for invalid token, got %q", got)
	}
}
