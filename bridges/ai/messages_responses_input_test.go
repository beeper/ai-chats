package ai

import (
	"testing"

	"github.com/openai/openai-go/v3/responses"
)

func TestPromptContextToResponsesInput_MultimodalUser(t *testing.T) {
	input := PromptContextToResponsesInput(UserPromptContext(
		PromptBlock{Type: PromptBlockText, Text: "hello"},
		PromptBlock{Type: PromptBlockImage, ImageB64: "aGVsbG8=", MimeType: "image/png"},
		PromptBlock{Type: PromptBlockFile, FileB64: "cGRm", Filename: "document.pdf"},
	))
	if len(input) != 1 {
		t.Fatalf("expected 1 input item, got %d", len(input))
	}

	item := input[0].OfMessage
	if item == nil {
		t.Fatalf("expected message input, got nil")
	}
	if item.Role != responses.EasyInputMessageRoleUser {
		t.Fatalf("expected user role, got %s", item.Role)
	}

	parts := item.Content.OfInputItemContentList
	if len(parts) == 0 {
		t.Fatalf("expected content parts for multimodal input")
	}

	foundText := false
	foundImage := false
	foundFile := false
	for _, part := range parts {
		if part.OfInputText != nil {
			foundText = true
			if part.OfInputText.Text != "hello" {
				t.Fatalf("expected text part to preserve content, got %#v", part.OfInputText.Text)
			}
		}
		if part.OfInputImage != nil {
			foundImage = true
			if part.OfInputImage.ImageURL.Value != "data:image/png;base64,aGVsbG8=" {
				t.Fatalf("expected image part data URL to preserve content, got %#v", part.OfInputImage.ImageURL.Value)
			}
		}
		if part.OfInputFile != nil {
			foundFile = true
			if part.OfInputFile.Filename.Value != "document.pdf" {
				t.Fatalf("expected file part filename document.pdf, got %#v", part.OfInputFile.Filename.Value)
			}
		}
	}

	if !foundText || !foundImage || !foundFile {
		t.Fatalf("expected text, image, and file parts (got text=%v image=%v file=%v)", foundText, foundImage, foundFile)
	}
}
