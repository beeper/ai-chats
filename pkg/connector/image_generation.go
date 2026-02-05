package connector

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/beeper/ai-bridge/pkg/shared/media"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// decodeBase64Image decodes a base64-encoded image and detects its MIME type.
// Handles both raw base64 and data URL format (data:image/png;base64,...).
func decodeBase64Image(b64Data string) ([]byte, string, error) {
	data, mimeType, err := media.DecodeBase64(b64Data)
	if err != nil {
		return nil, "", err
	}
	if !strings.HasPrefix(mimeType, "image/") {
		return nil, "", fmt.Errorf("unsupported media type: %s", mimeType)
	}
	return data, mimeType, nil
}

// sendGeneratedImage uploads an AI-generated image to Matrix and sends it as a message
func (oc *AIClient) sendGeneratedImage(
	ctx context.Context,
	portal *bridgev2.Portal,
	imageData []byte,
	mimeType string,
	turnID string,
) (id.EventID, string, error) {
	// Generate filename based on timestamp and mime type
	ext := extensionForMIME(mimeType, "png", map[string]string{
		"image/jpeg": "jpg",
		"image/webp": "webp",
		"image/gif":  "gif",
	})
	fileName := fmt.Sprintf("generated-%d.%s", time.Now().UnixMilli(), ext)
	return oc.sendGeneratedMedia(
		ctx,
		portal,
		imageData,
		mimeType,
		turnID,
		event.MsgImage,
		fileName,
		"com.beeper.ai.image_generation",
		false,
	)
}
