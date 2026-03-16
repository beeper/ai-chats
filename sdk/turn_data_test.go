package sdk

import (
	"testing"

	"github.com/beeper/agentremote"
)

func TestTurnDataFromUIMessageRoundTrip(t *testing.T) {
	ui := map[string]any{
		"id":   "turn-1",
		"role": "assistant",
		"metadata": map[string]any{
			"turn_id": "turn-1",
			"model":   "openai/gpt-5",
		},
		"bridgeHint": "keep-me",
		"parts": []any{
			map[string]any{"type": "text", "state": "done", "text": "hello"},
			map[string]any{
				"type":       "tool",
				"state":      "output-available",
				"toolCallId": "call_1",
				"toolName":   "search",
				"input":      map[string]any{"query": "matrix"},
				"output":     map[string]any{"result": "done"},
				"providerMetadata": map[string]any{
					"site_name": "Example",
				},
			},
		},
	}

	td, ok := TurnDataFromUIMessage(ui)
	if !ok {
		t.Fatalf("expected turn data")
	}
	if td.ID != "turn-1" || td.Role != "assistant" {
		t.Fatalf("unexpected identity: %#v", td)
	}
	if len(td.Parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(td.Parts))
	}
	if td.Extra["bridgeHint"] != "keep-me" {
		t.Fatalf("expected top-level extra to round-trip, got %#v", td.Extra)
	}
	if td.Parts[1].Extra["providerMetadata"] == nil {
		t.Fatalf("expected part extra to preserve providerMetadata, got %#v", td.Parts[1].Extra)
	}

	roundTrip := UIMessageFromTurnData(td)
	if got := roundTrip["id"]; got != "turn-1" {
		t.Fatalf("unexpected round-trip id: %#v", got)
	}
	if got := roundTrip["bridgeHint"]; got != "keep-me" {
		t.Fatalf("unexpected round-trip extra: %#v", got)
	}
	parts, ok := roundTrip["parts"].([]any)
	if !ok || len(parts) != 2 {
		t.Fatalf("expected 2 round-trip parts, got %#v", roundTrip["parts"])
	}
	toolPart, _ := parts[1].(map[string]any)
	if toolPart["providerMetadata"] == nil {
		t.Fatalf("expected part extra to survive round-trip, got %#v", toolPart)
	}
}

func TestBuildTurnDataFromUIMessageMergesRuntimeState(t *testing.T) {
	ui := map[string]any{
		"id":   "turn-1",
		"role": "assistant",
		"parts": []any{
			map[string]any{"type": "text", "text": "hello"},
		},
	}

	td := BuildTurnDataFromUIMessage(ui, TurnDataBuildOptions{
		Metadata:  map[string]any{"finish_reason": "stop"},
		Reasoning: "thinking",
		ToolCalls: []agentremote.ToolCallMetadata{{
			CallID:   "tool-1",
			ToolName: "search",
			ToolType: "function",
			Status:   "output-available",
			Output:   map[string]any{"ok": true},
		}},
		GeneratedFiles: []agentremote.GeneratedFileRef{{
			URL:      "mxc://file",
			MimeType: "image/png",
		}},
		ArtifactParts: []map[string]any{
			{"type": "source-url", "url": "https://example.com", "title": "Example"},
		},
	})

	if td.Metadata["finish_reason"] != "stop" {
		t.Fatalf("expected metadata merge, got %#v", td.Metadata)
	}
	if !TurnDataHasPartType(td, "reasoning") {
		t.Fatalf("expected reasoning part, got %#v", td.Parts)
	}
	if !TurnDataHasToolCall(td, "tool-1") {
		t.Fatalf("expected tool part, got %#v", td.Parts)
	}
	if !TurnDataHasURLPart(td, "file", "mxc://file") {
		t.Fatalf("expected generated file part, got %#v", td.Parts)
	}
	if !TurnDataHasURLPart(td, "source-url", "https://example.com") {
		t.Fatalf("expected source-url part, got %#v", td.Parts)
	}
}

func TestPromptMessagesFromTurnData(t *testing.T) {
	td := TurnData{
		Role: "assistant",
		Parts: []TurnPart{
			{Type: "text", Text: "hello"},
			{Type: "reasoning", Reasoning: "thinking"},
			{Type: "tool", ToolCallID: "tool-1", ToolName: "search", Input: map[string]any{"q": "matrix"}, Output: map[string]any{"done": true}},
		},
	}

	messages := PromptMessagesFromTurnData(td)
	if len(messages) != 2 {
		t.Fatalf("expected assistant + tool result, got %#v", messages)
	}
	if messages[0].Role != PromptRoleAssistant {
		t.Fatalf("unexpected assistant role %#v", messages[0])
	}
	if messages[1].Role != PromptRoleToolResult || messages[1].ToolCallID != "tool-1" {
		t.Fatalf("unexpected tool result %#v", messages[1])
	}
}

func TestTurnDataFromUserPromptMessagesPreservesInlineMedia(t *testing.T) {
	messages := []PromptMessage{{
		Role: PromptRoleUser,
		Blocks: []PromptBlock{
			{Type: PromptBlockText, Text: "describe these attachments"},
			{Type: PromptBlockImage, ImageB64: "aW1hZ2U=", MimeType: "image/png"},
			{Type: PromptBlockFile, FileB64: "data:application/pdf;base64,cGRm", Filename: "doc.pdf", MimeType: "application/pdf"},
			{Type: PromptBlockAudio, AudioB64: "YXVkaW8=", AudioFormat: "mp3", MimeType: "audio/mpeg"},
			{Type: PromptBlockVideo, VideoB64: "dmlkZW8=", MimeType: "video/mp4"},
		},
	}}

	td, ok := TurnDataFromUserPromptMessages(messages)
	if !ok {
		t.Fatal("expected user prompt messages to produce turn data")
	}
	if len(td.Parts) != 5 {
		t.Fatalf("expected 5 parts, got %#v", td.Parts)
	}

	roundTrip := PromptMessagesFromTurnData(td)
	if len(roundTrip) != 1 || len(roundTrip[0].Blocks) != 5 {
		t.Fatalf("expected one user message with 5 blocks, got %#v", roundTrip)
	}
	if got := roundTrip[0].Blocks[1].ImageB64; got != "aW1hZ2U=" {
		t.Fatalf("expected inline image to round-trip, got %#v", roundTrip[0].Blocks[1])
	}
	if got := roundTrip[0].Blocks[2].FileB64; got != "data:application/pdf;base64,cGRm" {
		t.Fatalf("expected inline file to round-trip, got %#v", roundTrip[0].Blocks[2])
	}
	if got := roundTrip[0].Blocks[3].AudioB64; got != "YXVkaW8=" {
		t.Fatalf("expected inline audio to round-trip, got %#v", roundTrip[0].Blocks[3])
	}
	if got := roundTrip[0].Blocks[3].AudioFormat; got != "mp3" {
		t.Fatalf("expected audio format to round-trip, got %#v", roundTrip[0].Blocks[3])
	}
	if got := roundTrip[0].Blocks[4].VideoB64; got != "dmlkZW8=" {
		t.Fatalf("expected inline video to round-trip, got %#v", roundTrip[0].Blocks[4])
	}
}
