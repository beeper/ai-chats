package providers

import (
	"testing"

	"github.com/beeper/ai-bridge/pkg/ai"
)

func TestCopilotHeaderHelpers(t *testing.T) {
	userOnly := []ai.Message{
		{Role: ai.RoleUser, Text: "hello"},
	}
	if got := InferCopilotInitiator(userOnly); got != "user" {
		t.Fatalf("expected user initiator, got %q", got)
	}

	agentFollowUp := []ai.Message{
		{Role: ai.RoleUser, Text: "hello"},
		{Role: ai.RoleAssistant, Content: []ai.ContentBlock{{Type: ai.ContentTypeText, Text: "hi"}}},
	}
	if got := InferCopilotInitiator(agentFollowUp); got != "agent" {
		t.Fatalf("expected agent initiator for assistant tail, got %q", got)
	}

	withImages := []ai.Message{
		{
			Role: ai.RoleUser,
			Content: []ai.ContentBlock{
				{Type: ai.ContentTypeImage, MimeType: "image/png", Data: "abc"},
			},
		},
	}
	if !HasCopilotVisionInput(withImages) {
		t.Fatalf("expected vision input detection for user image")
	}

	toolImages := []ai.Message{
		{
			Role: ai.RoleToolResult,
			Content: []ai.ContentBlock{
				{Type: ai.ContentTypeImage, MimeType: "image/png", Data: "abc"},
			},
		},
	}
	if !HasCopilotVisionInput(toolImages) {
		t.Fatalf("expected vision input detection for tool result image")
	}

	headers := BuildCopilotDynamicHeaders(agentFollowUp, true)
	if headers["X-Initiator"] != "agent" {
		t.Fatalf("expected X-Initiator=agent, got %#v", headers["X-Initiator"])
	}
	if headers["Openai-Intent"] != "conversation-edits" {
		t.Fatalf("expected Openai-Intent=conversation-edits")
	}
	if headers["Copilot-Vision-Request"] != "true" {
		t.Fatalf("expected Copilot-Vision-Request=true")
	}
}
