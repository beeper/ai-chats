package connector

import (
	"testing"

	"github.com/openai/openai-go/v3/responses"
)

func TestToOpenAIResponsesInput_MultimodalUser(t *testing.T) {
	msg := UnifiedMessage{
		Role: RoleUser,
		Content: []ContentPart{
			{Type: ContentTypeText, Text: "hello"},
			{Type: ContentTypeImage, ImageB64: "aGVsbG8=", MimeType: "image/png"},
			{Type: ContentTypePDF, PDFB64: "cGRm"},
		},
	}

	input := ToOpenAIResponsesInput([]UnifiedMessage{msg})
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
		}
		if part.OfInputImage != nil {
			foundImage = true
		}
		if part.OfInputFile != nil {
			foundFile = true
		}
	}

	if !foundText || !foundImage || !foundFile {
		t.Fatalf("expected text, image, and file parts (got text=%v image=%v file=%v)", foundText, foundImage, foundFile)
	}
}
