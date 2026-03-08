package connector

import (
	"maunium.net/go/mautrix/bridgev2/commands"
)

func requireClientMeta(ce *commands.Event) (*AIClient, *PortalMetadata, bool) {
	client := getAIClient(ce)
	meta := getPortalMeta(ce)
	if client == nil || meta == nil {
		ce.Reply("Couldn't load AI settings. Try again.")
		return nil, nil, false
	}
	return client, meta, true
}
