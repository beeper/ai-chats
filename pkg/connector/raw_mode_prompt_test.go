package connector

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/openai/openai-go/v3"
)

func TestRawModePrompt_HasSingleSystemPromptWithTimeAndWebSearch(t *testing.T) {
	client := &AIClient{
		connector: &OpenAIConnector{
			Config: Config{
				Messages: &MessagesConfig{
					DirectChat: &DirectChatConfig{HistoryLimit: 0},
				},
			},
		},
	}

	meta := &PortalMetadata{
		IsRawMode: true,
		// No SystemPrompt override: should use defaultRawModeSystemPrompt.
	}

	out, err := client.buildPromptWithLinkContext(context.Background(), nil, meta, "hello", nil, "")
	if err != nil {
		t.Fatalf("buildPromptWithLinkContext error: %v", err)
	}

	systemCount := 0
	systemText := ""
	for _, m := range out {
		if m.OfSystem != nil {
			systemCount++
			if m.OfSystem.Content.OfString.Valid() {
				systemText = strings.TrimSpace(m.OfSystem.Content.OfString.Value)
			}
		}
	}
	if systemCount != 1 {
		t.Fatalf("expected exactly 1 system message, got %d", systemCount)
	}
	if !strings.Contains(systemText, defaultRawModeSystemPrompt) {
		t.Fatalf("expected system prompt to include default raw prompt, got: %q", systemText)
	}
	if !strings.Contains(systemText, "Current time:") {
		t.Fatalf("expected system prompt to include current time line, got: %q", systemText)
	}
	if strings.Contains(systemText, "web_search") {
		t.Fatalf("did not expect system prompt to mention web_search when tools are not enabled, got: %q", systemText)
	}
}

func TestRawModePrompt_NoWebSearchHintEvenWhenConfigured(t *testing.T) {
	client := &AIClient{
		connector: &OpenAIConnector{
			Config: Config{
				Messages: &MessagesConfig{
					DirectChat: &DirectChatConfig{HistoryLimit: 0},
				},
				Tools: ToolProvidersConfig{
					Search: &SearchConfig{
						Brave: ProviderBraveConfig{APIKey: "test-key"},
					},
				},
			},
		},
	}

	meta := &PortalMetadata{
		IsRawMode: true,
		Capabilities: ModelCapabilities{
			SupportsToolCalling: true,
		},
	}

	out, err := client.buildPromptWithLinkContext(context.Background(), nil, meta, "hello", nil, "")
	if err != nil {
		t.Fatalf("buildPromptWithLinkContext error: %v", err)
	}

	systemText := ""
	for _, m := range out {
		if m.OfSystem != nil && m.OfSystem.Content.OfString.Valid() {
			systemText = strings.TrimSpace(m.OfSystem.Content.OfString.Value)
			break
		}
	}
	if systemText == "" {
		t.Fatalf("expected a system prompt")
	}
	if strings.Contains(systemText, "web_search") {
		t.Fatalf("raw mode should not advertise web_search (tools are never injected), got: %q", systemText)
	}
}

func TestRawModePrompt_LatestUserMessageUnchanged_NoLinkContext_NoMessageID(t *testing.T) {
	client := &AIClient{
		connector: &OpenAIConnector{
			Config: Config{
				Messages: &MessagesConfig{
					DirectChat: &DirectChatConfig{HistoryLimit: 0},
				},
				LinkPreviews: &LinkPreviewConfig{
					Enabled:         true,
					MaxURLsInbound:  5,
					MaxContentChars: 2000,
					FetchTimeout:    50 * time.Millisecond, // unused in raw mode
				},
				Memory: &MemoryConfig{
					InjectContext: true,
				},
			},
		},
	}

	meta := &PortalMetadata{IsRawMode: true}
	latest := "check this: https://example.com"

	out, err := client.buildPromptWithLinkContext(context.Background(), nil, meta, latest, nil, "$evt")
	if err != nil {
		t.Fatalf("buildPromptWithLinkContext error: %v", err)
	}

	// Expect final message is the last entry and equals latest (trimmed).
	if len(out) < 2 {
		t.Fatalf("expected at least system+user messages, got %d", len(out))
	}
	last := out[len(out)-1]
	if last.OfUser == nil || !last.OfUser.Content.OfString.Valid() {
		t.Fatalf("expected final message to be a user message, got %+v", last)
	}
	got := last.OfUser.Content.OfString.Value
	if got != strings.TrimSpace(latest) {
		t.Fatalf("expected latest user message unchanged, got %q want %q", got, strings.TrimSpace(latest))
	}
	if strings.Contains(strings.ToLower(got), "[message_id:") {
		t.Fatalf("did not expect message_id hint in raw mode, got %q", got)
	}
}

func TestBuildMatrixInboundBody_RawModeBypassesEnvelopeAndSenderMeta(t *testing.T) {
	client := &AIClient{}
	meta := &PortalMetadata{IsRawMode: true}

	got := client.buildMatrixInboundBody(context.Background(), nil, meta, nil, "  hi  ", "Alice", "Room", true)
	if got != "hi" {
		t.Fatalf("expected raw body only, got %q", got)
	}
}

func TestInjectMemoryContext_SkipsInRawMode(t *testing.T) {
	client := &AIClient{
		connector: &OpenAIConnector{
			Config: Config{
				Memory: &MemoryConfig{InjectContext: true},
			},
		},
	}

	in := []openai.ChatCompletionMessageParamUnion{
		openai.SystemMessage("system"),
	}
	meta := &PortalMetadata{IsRawMode: true}

	out := client.injectMemoryContext(context.Background(), nil, meta, in)
	if len(out) != len(in) {
		t.Fatalf("expected unchanged prompt length %d, got %d", len(in), len(out))
	}
}
