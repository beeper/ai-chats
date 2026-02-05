package connector

import (
	"context"
	"fmt"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// sendGeneratedAudio uploads TTS-generated audio to Matrix and sends it as a voice message.
func (oc *AIClient) sendGeneratedAudio(
	ctx context.Context,
	portal *bridgev2.Portal,
	audioData []byte,
	mimeType string,
	turnID string,
) (id.EventID, string, error) {
	// Determine file extension based on MIME type
	ext := extensionForMIME(mimeType, "mp3", map[string]string{
		"audio/aac":    "aac",
		"audio/x-aac":  "aac",
		"audio/aiff":   "aiff",
		"audio/x-aiff": "aiff",
		"audio/wave":   "wav",
		"audio/wav":    "wav",
		"audio/x-wav":  "wav",
		"audio/ogg":    "ogg",
		"audio/opus":   "opus",
		"audio/flac":   "flac",
		"audio/mp4":    "m4a",
		"audio/m4a":    "m4a",
		"audio/x-m4a":  "m4a",
	})
	fileName := fmt.Sprintf("tts-%d.%s", time.Now().UnixMilli(), ext)
	return oc.sendGeneratedMedia(
		ctx,
		portal,
		audioData,
		mimeType,
		turnID,
		event.MsgAudio,
		fileName,
		"com.beeper.ai.tts",
		true,
	)
}
