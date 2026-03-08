package connector

import (
	"context"
	"strings"
	"testing"

	airuntime "github.com/beeper/ai-bridge/pkg/runtime"
)

func TestBuildPromptWithLinkContext_InboundRuntimeMetadata(t *testing.T) {
	client := &AIClient{
		connector: &OpenAIConnector{
			Config: Config{
				Messages: &MessagesConfig{
					DirectChat: &DirectChatConfig{HistoryLimit: 0},
				},
			},
		},
	}
	meta := &PortalMetadata{}
	ctx := withInboundContext(context.Background(), airuntime.InboundContext{
		Provider:          "matrix",
		Surface:           "beeper-matrix",
		ChatType:          "group",
		ChatID:            "!room:test",
		ConversationLabel: "Team Room",
		SenderLabel:       "Alice",
		SenderID:          "@alice:test",
		MessageID:         "$evt",
		BodyForAgent:      "Alice: hello",
		BodyForCommands:   "hello",
	})

	out, err := client.buildPromptWithLinkContext(ctx, nil, meta, "Alice: hello", nil, "$evt")
	if err != nil {
		t.Fatalf("buildPromptWithLinkContext error: %v", err)
	}

	var trustedFound bool
	var lastUser string
	for _, msg := range out {
		if msg.OfSystem != nil && msg.OfSystem.Content.OfString.Valid() {
			if strings.Contains(msg.OfSystem.Content.OfString.Value, "Inbound Context (trusted metadata)") {
				trustedFound = true
			}
		}
		if msg.OfUser != nil && msg.OfUser.Content.OfString.Valid() {
			lastUser = msg.OfUser.Content.OfString.Value
		}
	}
	if !trustedFound {
		t.Fatalf("expected trusted inbound system prompt in message list")
	}
	if !strings.Contains(lastUser, "Conversation info (untrusted metadata):") {
		t.Fatalf("expected untrusted context prefix in user message, got %q", lastUser)
	}
	if !strings.Contains(lastUser, "Alice: hello") {
		t.Fatalf("expected sanitized user body in final message, got %q", lastUser)
	}
}

func TestBuildPromptWithLinkContext_SimpleModeSkipsInboundRuntimeMetadata(t *testing.T) {
	client := &AIClient{
		connector: &OpenAIConnector{
			Config: Config{
				Messages: &MessagesConfig{
					DirectChat: &DirectChatConfig{HistoryLimit: 0},
				},
			},
		},
	}
	meta := simpleModeTestMeta("openai/gpt-5")
	ctx := withInboundContext(context.Background(), airuntime.InboundContext{
		Provider:     "matrix",
		Surface:      "beeper-matrix",
		ChatType:     "direct",
		ChatID:       "!room:test",
		MessageID:    "$evt",
		BodyForAgent: "hello",
	})

	out, err := client.buildPromptWithLinkContext(ctx, nil, meta, "hello", nil, "$evt")
	if err != nil {
		t.Fatalf("buildPromptWithLinkContext error: %v", err)
	}

	systemCount := 0
	var lastUser string
	for _, msg := range out {
		if msg.OfSystem != nil {
			systemCount++
			if msg.OfSystem.Content.OfString.Valid() && strings.Contains(msg.OfSystem.Content.OfString.Value, "Inbound Context (trusted metadata)") {
				t.Fatalf("did not expect trusted inbound metadata system prompt in simple mode")
			}
		}
		if msg.OfUser != nil && msg.OfUser.Content.OfString.Valid() {
			lastUser = msg.OfUser.Content.OfString.Value
		}
	}
	if systemCount != 1 {
		t.Fatalf("expected exactly one system message in simple mode, got %d", systemCount)
	}
	if strings.Contains(lastUser, "Conversation info (untrusted metadata):") {
		t.Fatalf("did not expect untrusted inbound prefix in simple mode user message")
	}
}
