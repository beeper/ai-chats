package providers

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/beeper/ai-bridge/pkg/ai"
)

func TestStreamOpenAICompletions_MissingAPIKeyEmitsError(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "")
	stream := streamOpenAICompletions(ai.Model{
		ID:       "openai/gpt-4o-mini",
		Provider: "openrouter",
		API:      ai.APIOpenAICompletions,
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
	if _, err = stream.Next(ctx); err != io.EOF {
		t.Fatalf("expected EOF after terminal event, got %v", err)
	}
}

func TestMapChatCompletionFinishReason(t *testing.T) {
	cases := map[string]ai.StopReason{
		"stop":       ai.StopReasonStop,
		"length":     ai.StopReasonLength,
		"tool_calls": ai.StopReasonToolUse,
		"tool":       ai.StopReasonToolUse,
		"error":      ai.StopReasonError,
		"":           ai.StopReasonStop,
	}
	for in, want := range cases {
		if got := mapChatCompletionFinishReason(in); got != want {
			t.Fatalf("mapChatCompletionFinishReason(%q) = %q, want %q", in, got, want)
		}
	}
}
