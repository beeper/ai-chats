package connector

import (
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/commands"

	"github.com/beeper/ai-bridge/pkg/agents"
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

func requireClient(ce *commands.Event) (*AIClient, bool) {
	client := getAIClient(ce)
	if client == nil {
		ce.Reply("Couldn't load AI settings. Try again.")
		return nil, false
	}
	return client, true
}

func requirePortal(ce *commands.Event) (*bridgev2.Portal, bool) {
	if ce.Portal == nil {
		ce.Reply("This command can only be used in a room.")
		return nil, false
	}
	return ce.Portal, true
}

func rejectBossOverrides(ce *commands.Event, meta *PortalMetadata, message string) bool {
	if agents.IsBossAgent(resolveAgentID(meta)) {
		ce.Reply(message)
		return true
	}
	return false
}
