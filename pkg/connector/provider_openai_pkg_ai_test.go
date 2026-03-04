package connector

import (
	"context"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

func TestOpenAIProviderGenerate_UsesPkgAIBridgeWhenEnabled(t *testing.T) {
	t.Setenv("PI_USE_PKG_AI_PROVIDER_RUNTIME", "true")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_OAUTH_TOKEN", "")

	provider, err := NewOpenAIProviderWithBaseURL("", "https://api.anthropic.com", zerolog.Nop())
	if err != nil {
		t.Fatalf("unexpected provider init error: %v", err)
	}

	_, err = provider.Generate(context.Background(), GenerateParams{
		Model: "claude-sonnet-4-5",
		Messages: []UnifiedMessage{
			{
				Role:    RoleUser,
				Content: []ContentPart{{Type: ContentTypeText, Text: "hello"}},
			},
		},
	})
	if err == nil {
		t.Fatalf("expected pkg/ai runtime error without anthropic credentials")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "pkg/ai generation failed") {
		t.Fatalf("expected pkg/ai bridge error prefix, got %q", err.Error())
	}
}
