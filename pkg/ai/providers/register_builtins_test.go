package providers

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/beeper/ai-bridge/pkg/ai"
)

func TestRegisterBuiltInAPIProviders(t *testing.T) {
	ai.ClearAPIProviders()
	t.Cleanup(ai.ClearAPIProviders)

	RegisterBuiltInAPIProviders()
	providers := ai.GetAPIProviders()
	if len(providers) < 9 {
		t.Fatalf("expected builtin providers to be registered, got %d", len(providers))
	}

	stream, err := ai.Stream(ai.Model{
		ID:       "gpt-5",
		Provider: "openai",
		API:      ai.APIOpenAIResponses,
	}, ai.Context{}, nil)
	if err != nil {
		t.Fatalf("unexpected stream resolve error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	evt, err := stream.Next(ctx)
	if err != nil {
		t.Fatalf("expected terminal error event, got %v", err)
	}
	if evt.Type != ai.EventError {
		t.Fatalf("expected error event, got %s", evt.Type)
	}
	if evt.Error.StopReason != ai.StopReasonError {
		t.Fatalf("expected stopReason=error, got %s", evt.Error.StopReason)
	}
	if strings.Contains(strings.ToLower(evt.Error.ErrorMessage), "not implemented") {
		t.Fatalf("expected openai responses runtime implementation, got stub error: %q", evt.Error.ErrorMessage)
	}
	if _, err := stream.Next(ctx); err != io.EOF {
		t.Fatalf("expected EOF after terminal event, got %v", err)
	}

	completionsStream, err := ai.Stream(ai.Model{
		ID:       "openai/gpt-4o-mini",
		Provider: "openrouter",
		API:      ai.APIOpenAICompletions,
	}, ai.Context{}, nil)
	if err != nil {
		t.Fatalf("unexpected completions stream resolve error: %v", err)
	}
	completionsEvt, err := completionsStream.Next(ctx)
	if err != nil {
		t.Fatalf("expected completions terminal error event, got %v", err)
	}
	if completionsEvt.Type != ai.EventError {
		t.Fatalf("expected completions error event, got %s", completionsEvt.Type)
	}
	if strings.Contains(strings.ToLower(completionsEvt.Error.ErrorMessage), "not implemented") {
		t.Fatalf("expected openai completions runtime implementation, got stub error: %q", completionsEvt.Error.ErrorMessage)
	}

	codexStream, err := ai.Stream(ai.Model{
		ID:       "gpt-5.1-codex-mini",
		Provider: "openai-codex",
		API:      ai.APIOpenAICodexResponse,
	}, ai.Context{}, nil)
	if err != nil {
		t.Fatalf("unexpected codex stream resolve error: %v", err)
	}
	codexEvt, err := codexStream.Next(ctx)
	if err != nil {
		t.Fatalf("expected codex terminal error event, got %v", err)
	}
	if codexEvt.Type != ai.EventError {
		t.Fatalf("expected codex error event, got %s", codexEvt.Type)
	}
	if strings.Contains(strings.ToLower(codexEvt.Error.ErrorMessage), "not implemented") {
		t.Fatalf("expected codex runtime implementation, got stub error: %q", codexEvt.Error.ErrorMessage)
	}

	anthropicStream, err := ai.Stream(ai.Model{
		ID:       "claude-sonnet-4-5",
		Provider: "anthropic",
		API:      ai.APIAnthropicMessages,
	}, ai.Context{}, nil)
	if err != nil {
		t.Fatalf("unexpected anthropic stream resolve error: %v", err)
	}
	anthropicEvt, err := anthropicStream.Next(ctx)
	if err != nil {
		t.Fatalf("expected anthropic terminal error event, got %v", err)
	}
	if anthropicEvt.Type != ai.EventError {
		t.Fatalf("expected anthropic error event, got %s", anthropicEvt.Type)
	}
	if strings.Contains(strings.ToLower(anthropicEvt.Error.ErrorMessage), "not implemented") {
		t.Fatalf("expected anthropic runtime implementation, got stub error: %q", anthropicEvt.Error.ErrorMessage)
	}
}
