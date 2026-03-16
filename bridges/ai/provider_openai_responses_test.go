package ai

import (
	"context"
	"strings"
	"testing"

	"go.mau.fi/util/ptr"

	bridgesdk "github.com/beeper/agentremote/sdk"
)

func TestGenerateStreamRejectsUnsupportedResponsesPromptContext(t *testing.T) {
	provider := &OpenAIProvider{}
	params := GenerateParams{
		Context: PromptContext{
			PromptContext: bridgesdk.UserPromptContext(bridgesdk.PromptBlock{
				Type:        bridgesdk.PromptBlockAudio,
				AudioB64:    "YXVkaW8=",
				AudioFormat: "mp3",
				MimeType:    "audio/mpeg",
			}),
		},
	}

	events, err := provider.GenerateStream(context.Background(), params)
	if err == nil {
		t.Fatal("expected unsupported prompt context error")
	}
	if events != nil {
		t.Fatal("expected nil event channel on validation failure")
	}
	if !strings.Contains(err.Error(), "responses API does not support prompt context block types required by this request") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuildResponsesParamsPreservesExplicitZeroTemperature(t *testing.T) {
	provider := &OpenAIProvider{}
	params := provider.buildResponsesParams(GenerateParams{
		Model:       "gpt-5.2",
		Temperature: ptr.Ptr(0.0),
	})

	if !params.Temperature.Valid() || params.Temperature.Value != 0 {
		t.Fatalf("expected explicit zero temperature, got %#v", params.Temperature)
	}
}
