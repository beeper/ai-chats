package connector

import (
	"testing"

	"github.com/openai/openai-go/v3"
)

func TestIsGoogleModel(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		{"google/gemini-2.5-flash", true},
		{"google/gemini-3-pro-preview", true},
		{"gemini-pro", true},
		{"openrouter/google/gemini-flash", true},
		{"anthropic/claude-sonnet-4.5", false},
		{"openai/gpt-5", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := IsGoogleModel(tt.model); got != tt.want {
			t.Errorf("IsGoogleModel(%q) = %v, want %v", tt.model, got, tt.want)
		}
	}
}

func TestValidateGeminiTurns_Valid(t *testing.T) {
	prompt := []openai.ChatCompletionMessageParamUnion{
		openai.SystemMessage("system"),
		openai.UserMessage("hello"),
		openai.AssistantMessage("hi"),
		openai.UserMessage("how are you"),
	}
	if !ValidateGeminiTurns(prompt) {
		t.Fatal("expected valid turn sequence")
	}
}

func TestValidateGeminiTurns_ConsecutiveUser(t *testing.T) {
	prompt := []openai.ChatCompletionMessageParamUnion{
		openai.SystemMessage("system"),
		openai.UserMessage("hello"),
		openai.UserMessage("hello again"),
		openai.AssistantMessage("hi"),
	}
	if ValidateGeminiTurns(prompt) {
		t.Fatal("expected invalid for consecutive user messages")
	}
}

func TestValidateGeminiTurns_ConsecutiveAssistant(t *testing.T) {
	prompt := []openai.ChatCompletionMessageParamUnion{
		openai.UserMessage("hello"),
		openai.AssistantMessage("hi"),
		openai.AssistantMessage("I mean, hello"),
	}
	if ValidateGeminiTurns(prompt) {
		t.Fatal("expected invalid for consecutive assistant messages")
	}
}

func TestSanitizeGoogleTurnOrdering_MergesConsecutiveUser(t *testing.T) {
	prompt := []openai.ChatCompletionMessageParamUnion{
		openai.SystemMessage("system"),
		openai.UserMessage("hello"),
		openai.UserMessage("world"),
		openai.AssistantMessage("hi"),
	}
	result := SanitizeGoogleTurnOrdering(prompt)
	if !ValidateGeminiTurns(result) {
		t.Fatal("expected sanitized prompt to be valid")
	}
	// system + merged-user + assistant = 3
	if len(result) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(result))
	}
}

func TestSanitizeGoogleTurnOrdering_PrependsSyntheticUser(t *testing.T) {
	prompt := []openai.ChatCompletionMessageParamUnion{
		openai.SystemMessage("system"),
		openai.AssistantMessage("I was speaking"),
		openai.UserMessage("ok"),
	}
	result := SanitizeGoogleTurnOrdering(prompt)
	if !ValidateGeminiTurns(result) {
		t.Fatal("expected sanitized prompt to be valid")
	}
	// system + synthetic-user + assistant + user = 4
	if len(result) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(result))
	}
	// First non-system should be user
	if chatMessageRole(result[1]) != "user" {
		t.Fatalf("expected synthetic user message, got %s", chatMessageRole(result[1]))
	}
}

func TestSanitizeGoogleTurnOrdering_Empty(t *testing.T) {
	result := SanitizeGoogleTurnOrdering(nil)
	if result != nil {
		t.Fatal("expected nil for nil input")
	}
}

func TestSanitizeGoogleTurnOrdering_AlreadyValid(t *testing.T) {
	prompt := []openai.ChatCompletionMessageParamUnion{
		openai.UserMessage("hello"),
		openai.AssistantMessage("hi"),
	}
	result := SanitizeGoogleTurnOrdering(prompt)
	if len(result) != 2 {
		t.Fatalf("expected 2 messages for already-valid prompt, got %d", len(result))
	}
}

func TestChatMessageRole(t *testing.T) {
	tests := []struct {
		msg  openai.ChatCompletionMessageParamUnion
		want string
	}{
		{openai.SystemMessage("sys"), "system"},
		{openai.UserMessage("usr"), "user"},
		{openai.AssistantMessage("ast"), "assistant"},
	}
	for _, tt := range tests {
		if got := chatMessageRole(tt.msg); got != tt.want {
			t.Errorf("chatMessageRole() = %q, want %q", got, tt.want)
		}
	}
}
