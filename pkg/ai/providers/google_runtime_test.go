package providers

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"google.golang.org/genai"

	"github.com/beeper/ai-bridge/pkg/ai"
)

func TestStreamGoogleGenerativeAI_MissingAPIKeyEmitsError(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	stream := streamGoogleGenerativeAI(ai.Model{
		ID:       "gemini-2.5-flash",
		Provider: "google",
		API:      ai.APIGoogleGenerativeAI,
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

func TestStreamGoogleVertex_MissingEnvEmitsError(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_PROJECT", "")
	t.Setenv("GCLOUD_PROJECT", "")
	t.Setenv("GOOGLE_CLOUD_LOCATION", "")
	stream := streamGoogleVertex(ai.Model{
		ID:       "gemini-2.5-flash",
		Provider: "google-vertex",
		API:      ai.APIGoogleVertex,
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
	if !strings.Contains(strings.ToLower(evt.Error.ErrorMessage), "project") {
		t.Fatalf("expected missing project error, got %q", evt.Error.ErrorMessage)
	}
}

func TestGoogleRuntimeHelperMappings(t *testing.T) {
	if got := mapGoogleToolChoiceToGenAI("any"); got != genai.FunctionCallingConfigModeAny {
		t.Fatalf("expected any tool choice, got %q", got)
	}
	if got := mapGoogleToolChoiceToGenAI("none"); got != genai.FunctionCallingConfigModeNone {
		t.Fatalf("expected none tool choice, got %q", got)
	}
	if got := mapGoogleToolChoiceToGenAI("auto"); got != genai.FunctionCallingConfigModeAuto {
		t.Fatalf("expected auto tool choice, got %q", got)
	}

	if got := mapThinkingLevelToGenAI("xhigh"); got != genai.ThinkingLevelHigh {
		t.Fatalf("expected xhigh to clamp to high, got %q", got)
	}
	if got := mapThinkingLevelToGenAI("minimal"); got != genai.ThinkingLevelMinimal {
		t.Fatalf("expected minimal thinking level, got %q", got)
	}
}
