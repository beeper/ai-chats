package openclawconv

import "testing"

func TestAgentIDFromSessionKey(t *testing.T) {
	if got := AgentIDFromSessionKey("agent:main:discord:channel:123"); got != "main" {
		t.Fatalf("expected main, got %q", got)
	}
	if got := AgentIDFromSessionKey("main"); got != "" {
		t.Fatalf("expected empty agent id, got %q", got)
	}
}

func TestExtractMessageText(t *testing.T) {
	msg := map[string]any{
		"content": []any{
			map[string]any{"type": "input_text", "text": "hello"},
			map[string]any{"type": "output_text", "text": "world"},
		},
	}
	if got := ExtractMessageText(msg); got != "hello\n\nworld" {
		t.Fatalf("unexpected text: %q", got)
	}
}

func TestIsAttachmentBlock(t *testing.T) {
	if IsAttachmentBlock(map[string]any{"type": "output_text", "text": "hello"}) {
		t.Fatal("output_text should not be treated as attachment")
	}
	if IsAttachmentBlock(map[string]any{"type": "toolCall", "id": "call-1"}) {
		t.Fatal("toolCall should not be treated as attachment")
	}
	if !IsAttachmentBlock(map[string]any{
		"type":   "input_file",
		"source": map[string]any{"type": "url", "url": "https://example.com/file.txt"},
	}) {
		t.Fatal("input_file should be treated as attachment")
	}
	if !IsAttachmentBlock(map[string]any{
		"type": "file",
		"file": map[string]any{"url": "https://example.com/file.txt"},
	}) {
		t.Fatal("nested file map should be treated as attachment")
	}
	if !IsAttachmentBlock(map[string]any{
		"type":        "audio",
		"fileName":    "clip.mp3",
		"contentType": "audio/mpeg",
	}) {
		t.Fatal("audio block with filename/mime should be treated as attachment")
	}
	if !IsAttachmentBlock(map[string]any{
		"type": "image",
		"src":  map[string]any{"url": "https://example.com/image.png"},
	}) {
		t.Fatal("src map should be treated as attachment")
	}
}
