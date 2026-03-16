package media

import (
	"mime"
	"strings"

	"maunium.net/go/mautrix/event"

	"github.com/beeper/agentremote/pkg/shared/stringutil"
)

func MessageTypeForMIME(mimeType string) event.MessageType {
	mimeType = stringutil.NormalizeMimeType(mimeType)
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

func FallbackFilenameForMIME(mimeType string) string {
	mimeType = stringutil.NormalizeMimeType(mimeType)
	exts, _ := mime.ExtensionsByType(mimeType)
	if len(exts) > 0 {
		return "file" + exts[0]
	}
	return "file"
}
