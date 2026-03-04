package providers

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"

	"github.com/beeper/ai-bridge/pkg/ai"
)

func TestStreamAnthropicMessages_MissingAPIKeyEmitsError(t *testing.T) {
	t.Setenv("ANTHROPIC_OAUTH_TOKEN", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	stream := streamAnthropicMessages(ai.Model{
		ID:       "claude-sonnet-4-5",
		Provider: "anthropic",
		API:      ai.APIAnthropicMessages,
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

func TestMapAnthropicStopReason(t *testing.T) {
	cases := map[anthropic.StopReason]ai.StopReason{
		anthropic.StopReasonEndTurn:      ai.StopReasonStop,
		anthropic.StopReasonStopSequence: ai.StopReasonStop,
		anthropic.StopReasonMaxTokens:    ai.StopReasonLength,
		anthropic.StopReasonToolUse:      ai.StopReasonToolUse,
	}
	for in, want := range cases {
		if got := mapAnthropicStopReason(in); got != want {
			t.Fatalf("mapAnthropicStopReason(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMapAnthropicThinkingEffort(t *testing.T) {
	if got := mapAnthropicThinkingEffort("claude-opus-4-6", ai.ThinkingXHigh); got != "max" {
		t.Fatalf("expected xhigh on opus 4.6 to map to max, got %q", got)
	}
	if got := mapAnthropicThinkingEffort("claude-sonnet-4-6", ai.ThinkingXHigh); got != "high" {
		t.Fatalf("expected xhigh on sonnet 4.6 to map to high, got %q", got)
	}
	if got := mapAnthropicThinkingEffort("claude-sonnet-4-5", ai.ThinkingMinimal); got != "low" {
		t.Fatalf("expected minimal to map to low, got %q", got)
	}
}

func TestSupportsAdaptiveThinkingModel(t *testing.T) {
	if !supportsAdaptiveThinkingModel("claude-opus-4-6") {
		t.Fatalf("expected opus 4.6 to support adaptive thinking")
	}
	if !supportsAdaptiveThinkingModel("claude-sonnet-4.6") {
		t.Fatalf("expected sonnet 4.6 to support adaptive thinking")
	}
	if supportsAdaptiveThinkingModel("claude-sonnet-4-5") {
		t.Fatalf("did not expect sonnet 4.5 to support adaptive thinking")
	}
}
