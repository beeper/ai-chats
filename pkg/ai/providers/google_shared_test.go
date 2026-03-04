package providers

import (
	"strings"
	"testing"
	"time"

	"github.com/beeper/ai-bridge/pkg/ai"
)

func TestIsThinkingPartAndRetainSignature(t *testing.T) {
	if !IsThinkingPart(GooglePart{Thought: true}) {
		t.Fatalf("expected thought=true to be thinking part")
	}
	if IsThinkingPart(GooglePart{Thought: false, ThoughtSignature: "opaque"}) {
		t.Fatalf("thoughtSignature alone must not mark part as thinking")
	}

	first := RetainThoughtSignature("", "sig-1")
	if first != "sig-1" {
		t.Fatalf("expected initial signature to be retained")
	}
	second := RetainThoughtSignature(first, "")
	if second != "sig-1" {
		t.Fatalf("expected previous signature retained when incoming empty")
	}
	third := RetainThoughtSignature(second, "sig-2")
	if third != "sig-2" {
		t.Fatalf("expected signature to update when incoming non-empty")
	}
}

func TestConvertMessages_ConvertsUnsignedToolCallsToHistoricalTextForGemini3(t *testing.T) {
	now := time.Now().UnixMilli()
	model := ai.Model{
		ID:        "gemini-3-pro-preview",
		Name:      "Gemini 3 Pro Preview",
		API:       ai.APIGoogleGenerativeAI,
		Provider:  "google",
		BaseURL:   "https://generativelanguage.googleapis.com",
		Reasoning: true,
		Input:     []string{"text"},
	}
	context := ai.Context{
		Messages: []ai.Message{
			{Role: ai.RoleUser, Text: "Hi", Timestamp: now},
			{
				Role:       ai.RoleAssistant,
				Provider:   "google-antigravity",
				API:        ai.APIGoogleGeminiCLI,
				Model:      "claude-sonnet-4-20250514",
				StopReason: ai.StopReasonStop,
				Content: []ai.ContentBlock{
					{
						Type:      ai.ContentTypeToolCall,
						ID:        "call_1",
						Name:      "bash",
						Arguments: map[string]any{"command": "ls -la"},
					},
				},
				Timestamp: now,
			},
		},
	}

	contents := ConvertGoogleMessages(model, context)
	var toolTurn *GoogleContent
	for i := len(contents) - 1; i >= 0; i-- {
		if contents[i].Role == "model" {
			toolTurn = &contents[i]
			break
		}
	}
	if toolTurn == nil {
		t.Fatalf("expected model content turn")
	}
	for _, part := range toolTurn.Parts {
		if part.FunctionCall != nil {
			t.Fatalf("expected no function call for unsigned tool call in gemini-3")
		}
	}
	joined := ""
	for _, part := range toolTurn.Parts {
		joined += part.Text + "\n"
	}
	if !strings.Contains(joined, "Historical context") ||
		!strings.Contains(joined, "bash") ||
		!strings.Contains(joined, "ls -la") ||
		!strings.Contains(joined, "Do not mimic this format") {
		t.Fatalf("unexpected historical context text: %s", joined)
	}
}
