package e2e

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/beeper/ai-bridge/pkg/ai"
	"github.com/beeper/ai-bridge/pkg/ai/providers"
)

func TestAbortE2E_OpenAIStream(t *testing.T) {
	requirePIAIE2E(t)
	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if apiKey == "" {
		t.Skip("OPENAI_API_KEY is not set")
	}
	model := openAIE2EModel()
	providers.ResetAPIProviders()

	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	readCtx, cancelRead := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancelRead()

	stream, err := ai.Stream(model, ai.Context{
		Messages: []ai.Message{
			{
				Role:      ai.RoleUser,
				Text:      "Write a long explanation (at least 30 lines) about why unit tests are valuable.",
				Timestamp: time.Now().UnixMilli(),
			},
		},
	}, &ai.StreamOptions{
		APIKey:    apiKey,
		Ctx:       runCtx,
		MaxTokens: 2048,
	})
	if err != nil {
		t.Fatalf("stream creation failed: %v", err)
	}

	cancelled := false
	for {
		evt, nextErr := stream.Next(readCtx)
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			t.Fatalf("stream read failed: %v", nextErr)
		}
		if !cancelled && evt.Type == ai.EventTextDelta && strings.TrimSpace(evt.Delta) != "" {
			cancelRun()
			cancelled = true
		}
	}

	if !cancelled {
		t.Skip("stream completed before cancellation could be triggered")
	}
	result, resultErr := stream.Result()
	if resultErr != nil {
		t.Fatalf("stream result failed: %v", resultErr)
	}
	if result.StopReason != ai.StopReasonAborted {
		t.Fatalf("expected stop reason %q after cancel, got %q (error: %s)", ai.StopReasonAborted, result.StopReason, result.ErrorMessage)
	}
}
