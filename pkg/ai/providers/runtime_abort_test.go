package providers

import (
	"context"
	"errors"
	"testing"

	"github.com/beeper/ai-bridge/pkg/ai"
)

func TestIsContextAborted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !isContextAborted(ctx, nil) {
		t.Fatalf("expected canceled context to be treated as aborted")
	}
	if !isContextAborted(context.Background(), context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded error to be treated as aborted")
	}
	if !isContextAborted(context.Background(), errors.New("request failed: context canceled")) {
		t.Fatalf("expected context canceled message to be treated as aborted")
	}
	if isContextAborted(context.Background(), errors.New("provider rejected request")) {
		t.Fatalf("did not expect non-cancellation error to be treated as aborted")
	}
}

func TestPushProviderAborted(t *testing.T) {
	stream := ai.NewAssistantMessageEventStream(1)
	model := ai.Model{
		ID:       "gpt-5-mini",
		Provider: "openai",
		API:      ai.APIOpenAIResponses,
	}

	pushProviderAborted(stream, model)

	msg, err := stream.Result()
	if err != nil {
		t.Fatalf("expected aborted result without error, got %v", err)
	}
	if msg.StopReason != ai.StopReasonAborted {
		t.Fatalf("expected stop reason %q, got %q", ai.StopReasonAborted, msg.StopReason)
	}
}
