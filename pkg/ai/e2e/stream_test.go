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

func TestGenerateE2E_OpenAIComplete(t *testing.T) {
	requirePIAIE2E(t)
	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if apiKey == "" {
		t.Skip("OPENAI_API_KEY is not set")
	}
	model := openAIE2EModel()
	providers.ResetAPIProviders()

	response, err := ai.Complete(model, ai.Context{
		Messages: []ai.Message{
			{
				Role:      ai.RoleUser,
				Text:      "Reply with the single word OK.",
				Timestamp: time.Now().UnixMilli(),
			},
		},
	}, &ai.StreamOptions{APIKey: apiKey})
	if err != nil {
		t.Fatalf("complete failed: %v", err)
	}
	if response.StopReason == ai.StopReasonError {
		t.Fatalf("unexpected error stop reason: %s", response.ErrorMessage)
	}
	if len(response.Content) == 0 {
		t.Fatalf("expected non-empty response content")
	}
	text := strings.ToLower(strings.TrimSpace(firstText(response)))
	if text == "" || !strings.Contains(text, "ok") {
		t.Fatalf("expected response text to contain 'ok', got %q", text)
	}
}

func TestGenerateE2E_OpenAIStream(t *testing.T) {
	requirePIAIE2E(t)
	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if apiKey == "" {
		t.Skip("OPENAI_API_KEY is not set")
	}
	model := openAIE2EModel()
	providers.ResetAPIProviders()

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
		APIKey: apiKey,
		Ctx:    ctx,
	})
	if err != nil {
		t.Fatalf("stream creation failed: %v", err)
	}

	receivedDelta := false
	receivedDone := false
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

	message, resultErr := stream.Result()
	if resultErr != nil {
		t.Fatalf("stream result failed: %v", resultErr)
	}
	if message.StopReason == ai.StopReasonError {
		t.Fatalf("unexpected error stop reason: %s", message.ErrorMessage)
	}
	if !receivedDone {
		t.Fatalf("expected done event before stream close")
	}
	if !receivedDelta {
		t.Fatalf("expected at least one text delta event")
	}
}

func openAIE2EModel() ai.Model {
	modelID := strings.TrimSpace(os.Getenv("PI_AI_E2E_OPENAI_MODEL"))
	if modelID == "" {
		modelID = "gpt-4o-mini"
	}
	baseURL := strings.TrimSpace(os.Getenv("PI_AI_E2E_OPENAI_BASE_URL"))
	return ai.Model{
		ID:       modelID,
		Name:     modelID,
		API:      ai.APIOpenAIResponses,
		Provider: "openai",
		BaseURL:  baseURL,
	}
}

func firstText(message ai.Message) string {
	for _, block := range message.Content {
		if block.Type == ai.ContentTypeText {
			return block.Text
		}
	}
	return ""
}
