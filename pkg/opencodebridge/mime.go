package opencodebridge

import (
	"strings"

	"maunium.net/go/mautrix/event"
)

func normalizeMimeType(mimeType string) string {
	lower := strings.ToLower(strings.TrimSpace(mimeType))
	if lower == "" {
		return lower
	}
	if semi := strings.IndexByte(lower, ';'); semi >= 0 {
		return strings.TrimSpace(lower[:semi])
	}
	return lower
}

func messageTypeForMIME(mimeType string) event.MessageType {
	mimeType = strings.ToLower(strings.TrimSpace(mimeType))
	switch {
	case strings.HasPrefix(mimeType, "image/"):
		return event.MsgImage
	case strings.HasPrefix(mimeType, "audio/"):
		return event.MsgAudio
	case strings.HasPrefix(mimeType, "video/"):
		return event.MsgVideo
	default:
		return event.MsgFile
	}
}
