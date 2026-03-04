package providers

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/openai/openai-go/v3/responses"

	"github.com/beeper/ai-bridge/pkg/ai"
)

func TestStreamOpenAIResponses_MissingAPIKeyEmitsError(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	stream := streamOpenAIResponses(ai.Model{
		ID:       "gpt-4.1-mini",
		Provider: "openai",
		API:      ai.APIOpenAIResponses,
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

func TestParseToolArguments(t *testing.T) {
	if got := parseToolArguments(""); len(got) != 0 {
		t.Fatalf("expected empty map for empty args, got %#v", got)
	}
	valid := parseToolArguments(`{"x":1}`)
	if val, ok := valid["x"]; !ok || val.(float64) != 1 {
		t.Fatalf("expected parsed JSON args, got %#v", valid)
	}
	invalid := parseToolArguments("{oops")
	if invalid["_raw"] != "{oops" {
		t.Fatalf("expected raw fallback on invalid JSON, got %#v", invalid)
	}
}

func TestMapOpenAIResponseStatus(t *testing.T) {
	cases := map[responses.ResponseStatus]ai.StopReason{
		responses.ResponseStatusCompleted:  ai.StopReasonStop,
		responses.ResponseStatusInProgress: ai.StopReasonLength,
		responses.ResponseStatusIncomplete: ai.StopReasonLength,
		responses.ResponseStatusCancelled:  ai.StopReasonAborted,
		responses.ResponseStatusFailed:     ai.StopReasonError,
	}
	for in, want := range cases {
		if got := mapOpenAIResponseStatus(in); got != want {
			t.Fatalf("mapOpenAIResponseStatus(%q) = %q, want %q", in, got, want)
		}
	}
}
