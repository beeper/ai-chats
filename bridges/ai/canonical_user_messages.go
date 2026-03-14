package ai

import (
	"strings"

	"github.com/beeper/agentremote/sdk"
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
	if (len(meta.CanonicalPromptMessages) > 0 && meta.CanonicalPromptSchema == canonicalPromptSchemaV1) ||
		(len(meta.CanonicalTurnData) > 0 && meta.CanonicalTurnSchema == sdk.CanonicalTurnDataSchemaV1) {
		return
	}

	body := strings.TrimSpace(meta.Body)
	if body != "" {
		setCanonicalPromptMessages(meta, textPromptMessage(body))
	}
}
