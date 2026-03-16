package ai

import (
	"strings"

	"maunium.net/go/mautrix/bridgev2/database"
)

func ensureCanonicalUserMessage(msg *database.Message) {
	if msg == nil {
		return
	}
	meta, ok := msg.Metadata.(*MessageMetadata)
	if !ok || meta == nil || strings.TrimSpace(meta.Role) != "user" {
		return
	}
	if len(meta.CanonicalTurnData) > 0 {
		return
	}

	body := strings.TrimSpace(meta.Body)
	if body != "" {
		setCanonicalTurnDataFromPromptMessages(meta, textPromptMessage(body))
	}
}
