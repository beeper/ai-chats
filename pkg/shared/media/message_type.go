package media

import (
	"strings"

	"maunium.net/go/mautrix/event"
)

func MessageTypeForMIME(mimeType string) event.MessageType {
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
