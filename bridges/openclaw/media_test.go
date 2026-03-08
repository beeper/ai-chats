package openclaw

import (
	"context"
	"testing"
)

func TestOpenClawAgentIDFromSessionKey(t *testing.T) {
	if got := openClawAgentIDFromSessionKey("agent:main:discord:channel:123"); got != "main" {
		t.Fatalf("expected main, got %q", got)
	}
	if got := openClawAgentIDFromSessionKey("main"); got != "" {
		t.Fatalf("expected empty agent id, got %q", got)
	}
}

func TestExtractMessageTextOpenResponsesParts(t *testing.T) {
	msg := map[string]any{
		"content": []any{
			map[string]any{"type": "input_text", "text": "hello"},
			map[string]any{"type": "output_text", "text": "world"},
		},
	}
	if got := extractMessageText(msg); got != "hello\n\nworld" {
		t.Fatalf("unexpected extracted text: %q", got)
	}
}

func TestOpenClawAttachmentSourceFromBlock(t *testing.T) {
	block := map[string]any{
		"type": "input_file",
		"source": map[string]any{
			"type":       "base64",
			"media_type": "image/png",
			"data":       "Zm9v",
			"filename":   "dot.png",
		},
	}
	source := openClawAttachmentSourceFromBlock(block)
	if source == nil {
		t.Fatal("expected source")
	}
	if source.Kind != "base64" || source.FileName != "dot.png" || source.MimeType != "image/png" {
		t.Fatalf("unexpected source: %#v", source)
	}
}

func TestIsOpenClawAttachmentBlock(t *testing.T) {
	if isOpenClawAttachmentBlock(map[string]any{"type": "output_text", "text": "hello"}) {
		t.Fatal("output_text should not be treated as attachment")
	}
	if isOpenClawAttachmentBlock(map[string]any{"type": "toolCall", "id": "call-1"}) {
		t.Fatal("toolCall should not be treated as attachment")
	}
	if !isOpenClawAttachmentBlock(map[string]any{
		"type":   "input_file",
		"source": map[string]any{"type": "url", "url": "https://example.com/file.txt"},
	}) {
		t.Fatal("input_file should be treated as attachment")
	}
}

func TestOpenClawHistoryUIPartsToolCall(t *testing.T) {
	parts := openClawHistoryUIParts(map[string]any{
		"content": []any{
			map[string]any{
				"type":      "toolCall",
				"id":        "call-1",
				"name":      "bash",
				"arguments": map[string]any{"cmd": "ls"},
			},
		},
	}, "assistant")
	if len(parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(parts))
	}
	if parts[0]["type"] != "dynamic-tool" || parts[0]["toolCallId"] != "call-1" {
		t.Fatalf("unexpected part: %#v", parts[0])
	}
}

func TestOpenClawHistoryUIPartsToolResult(t *testing.T) {
	parts := openClawHistoryUIParts(map[string]any{
		"toolCallId": "call-1",
		"toolName":   "bash",
		"isError":    false,
		"details":    map[string]any{"stdout": "ok"},
		"content":    []any{map[string]any{"type": "text", "text": "ok"}},
	}, "toolresult")
	if len(parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(parts))
	}
	if parts[0]["state"] != "output-available" {
		t.Fatalf("unexpected tool result part: %#v", parts[0])
	}
}

func TestNormalizeOpenClawUsage(t *testing.T) {
	usage := normalizeOpenClawUsage(map[string]any{
		"input":           float64(10),
		"outputTokens":    int64(4),
		"reasoningTokens": int64(2),
		"total":           int64(16),
	})
	if usage["prompt_tokens"] != int64(10) {
		t.Fatalf("expected prompt_tokens=10, got %#v", usage["prompt_tokens"])
	}
	if usage["completion_tokens"] != int64(4) {
		t.Fatalf("expected completion_tokens=4, got %#v", usage["completion_tokens"])
	}
	if usage["reasoning_tokens"] != int64(2) {
		t.Fatalf("expected reasoning_tokens=2, got %#v", usage["reasoning_tokens"])
	}
	if usage["total_tokens"] != int64(16) {
		t.Fatalf("expected total_tokens=16, got %#v", usage["total_tokens"])
	}
}

func TestOpenClawAttachmentSourceFromNestedFileMap(t *testing.T) {
	block := map[string]any{
		"type": "file",
		"file": map[string]any{
			"url":      "https://example.com/doc.txt",
			"mimeType": "text/plain",
			"name":     "doc.txt",
		},
	}
	source := openClawAttachmentSourceFromBlock(block)
	if source == nil {
		t.Fatal("expected source")
	}
	if source.Kind != "url" || source.URL != "https://example.com/doc.txt" || source.FileName != "doc.txt" {
		t.Fatalf("unexpected source: %#v", source)
	}
}

func TestTopicForPortal(t *testing.T) {
	oc := &OpenClawClient{}
	topic := oc.topicForPortal(&PortalMetadata{
		OpenClawChannel:            "discord",
		OpenClawSubject:            "Support",
		ModelProvider:              "openai",
		Model:                      "gpt-5",
		OpenClawLastMessagePreview: "hello there",
		HistoryMode:                "recent_only",
	})
	want := "discord | Support | openai | gpt-5 | Recent: hello there | History: recent_only"
	if topic != want {
		t.Fatalf("unexpected topic: %q", topic)
	}
}

func TestOpenClawApprovalResolvedText(t *testing.T) {
	if got := openClawApprovalResolvedText("deny"); got != "Tool approval denied" {
		t.Fatalf("unexpected deny text: %q", got)
	}
}

func TestRecoverRunTextEmptyWithoutGateway(t *testing.T) {
	mgr := &openClawManager{}
	if text := mgr.recoverRunText(context.Background(), "", "turn-1"); text != "" {
		t.Fatalf("expected empty text, got %q", text)
	}
}
