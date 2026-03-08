package opencodebridge

import (
	"maunium.net/go/mautrix/event"

	"github.com/beeper/ai-bridge/pkg/shared/media"
)

func messageTypeForMIME(mimeType string) event.MessageType {
	return media.MessageTypeForMIME(mimeType)
}
