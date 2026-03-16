package ai

import (
	"maunium.net/go/mautrix/bridgev2/commands"
	"maunium.net/go/mautrix/event"
)

func requireClientMeta(ce *commands.Event) (*AIClient, *PortalMetadata, bool) {
	client := getAIClient(ce)
	meta := getPortalMeta(ce)
	if client == nil || meta == nil {
		message := "Couldn't load AI settings. Try again."
		reason := event.MessageStatusGenericError
		if ce != nil && ce.Portal != nil {
			message = "You're not logged in in this portal."
			reason = event.MessageStatusNoPermission
		}
		markCommandFailure(ce, message, reason)
		ce.Reply("%s", message)
		return nil, nil, false
	}
	return client, meta, true
}
