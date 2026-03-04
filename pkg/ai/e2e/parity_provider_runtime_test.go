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

func TestGenerateE2E_AnthropicComplete(t *testing.T) {
	requirePIAIE2E(t)
	apiKey := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY"))
	if apiKey == "" {
		t.Skip("ANTHROPIC_API_KEY is not set")
	}
	model := anthropicE2EModel()
	providers.ResetAPIProviders()

	response, err := ai.Complete(model, ai.Context{
		Messages: []ai.Message{
			{
				Role:      ai.RoleUser,
				Text:      "Reply with the single word OK.",
				Timestamp: time.Now().UnixMilli(),
			},
		},
	}, &ai.StreamOptions{
		APIKey:    apiKey,
		MaxTokens: 128,
	})
	if err != nil {
		t.Fatalf("complete failed: %v", err)
	}
	if response.StopReason == ai.StopReasonError {
		t.Fatalf("unexpected error stop reason: %s", response.ErrorMessage)
	}
	if strings.TrimSpace(firstText(response)) == "" {
		t.Fatalf("expected non-empty text response")
	}
}

func TestGenerateE2E_GoogleComplete(t *testing.T) {
	requirePIAIE2E(t)
	apiKey := strings.TrimSpace(os.Getenv("GEMINI_API_KEY"))
	if apiKey == "" {
		t.Skip("GEMINI_API_KEY is not set")
	}
	model := googleE2EModel()
	providers.ResetAPIProviders()

	response, err := ai.Complete(model, ai.Context{
		Messages: []ai.Message{
			{
				Role:      ai.RoleUser,
				Text:      "Reply with the single word OK.",
				Timestamp: time.Now().UnixMilli(),
			},
		},
	}, &ai.StreamOptions{
		APIKey:    apiKey,
		MaxTokens: 128,
	})
	if err != nil {
		t.Fatalf("complete failed: %v", err)
	}
	if response.StopReason == ai.StopReasonError {
		t.Fatalf("unexpected error stop reason: %s", response.ErrorMessage)
	}
	if strings.TrimSpace(firstText(response)) == "" {
		t.Fatalf("expected non-empty text response")
	}
}

func TestGenerateE2E_AnthropicStream(t *testing.T) {
	requirePIAIE2E(t)
	apiKey := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY"))
	if apiKey == "" {
		t.Skip("ANTHROPIC_API_KEY is not set")
	}
	providers.ResetAPIProviders()
	runBasicStreamE2E(t, anthropicE2EModel(), apiKey)
}

func TestGenerateE2E_GoogleStream(t *testing.T) {
	requirePIAIE2E(t)
	apiKey := strings.TrimSpace(os.Getenv("GEMINI_API_KEY"))
	if apiKey == "" {
		t.Skip("GEMINI_API_KEY is not set")
	}
	providers.ResetAPIProviders()
	runBasicStreamE2E(t, googleE2EModel(), apiKey)
}

func runBasicStreamE2E(t *testing.T, model ai.Model, apiKey string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	stream, err := ai.Stream(model, ai.Context{
		Messages: []ai.Message{
			{
				Role:      ai.RoleUser,
				Text:      "Reply with the single word OK.",
				Timestamp: time.Now().UnixMilli(),
			},
		},
	}, &ai.StreamOptions{
		APIKey:    apiKey,
		MaxTokens: 128,
		Ctx:       ctx,
	})
	if err != nil {
		t.Fatalf("stream creation failed: %v", err)
	}

	receivedDone := false
	receivedDelta := false
	for {
		evt, nextErr := stream.Next(ctx)
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			t.Fatalf("stream read failed: %v", nextErr)
		}
		switch evt.Type {
		case ai.EventTextDelta:
			if strings.TrimSpace(evt.Delta) != "" {
				receivedDelta = true
			}
		case ai.EventDone:
			receivedDone = true
		case ai.EventError:
			t.Fatalf("unexpected stream error: %s", evt.Error.ErrorMessage)
		}
	}

	response, err := stream.Result()
	if err != nil {
		t.Fatalf("stream result failed: %v", err)
	}
	if response.StopReason == ai.StopReasonError {
		t.Fatalf("unexpected stream stop error: %s", response.ErrorMessage)
	}
	if !receivedDone {
		t.Fatalf("expected done event")
	}
	if !receivedDelta && strings.TrimSpace(firstText(response)) == "" {
		t.Fatalf("expected either streamed deltas or non-empty final text")
	}
}

func anthropicE2EModel() ai.Model {
	modelID := strings.TrimSpace(os.Getenv("PI_AI_E2E_ANTHROPIC_MODEL"))
	if modelID == "" {
		modelID = "claude-3-5-haiku-latest"
	}
	baseURL := strings.TrimSpace(os.Getenv("PI_AI_E2E_ANTHROPIC_BASE_URL"))
	return ai.Model{
		ID:       modelID,
		Name:     modelID,
		API:      ai.APIAnthropicMessages,
		Provider: "anthropic",
		BaseURL:  baseURL,
	}
}

func googleE2EModel() ai.Model {
	modelID := strings.TrimSpace(os.Getenv("PI_AI_E2E_GOOGLE_MODEL"))
	if modelID == "" {
		modelID = "gemini-2.5-flash"
	}
	baseURL := strings.TrimSpace(os.Getenv("PI_AI_E2E_GOOGLE_BASE_URL"))
	return ai.Model{
		ID:       modelID,
		Name:     modelID,
		API:      ai.APIGoogleGenerativeAI,
		Provider: "google",
		BaseURL:  baseURL,
	}
}
