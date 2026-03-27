package ai

import (
	"context"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/commands"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/agentremote/bridges/ai/commandregistry"
	bridgesdk "github.com/beeper/agentremote/sdk"
)

// HelpSectionAI is the help section for AI-related commands.
var HelpSectionAI = commands.HelpSection{
	Name:  "AI Chat",
	Order: 30,
}

func resolveLoginForCommand(
	ctx context.Context,
	portal *bridgev2.Portal,
	user *bridgev2.User,
	defaultLogin *bridgev2.UserLogin,
	br *bridgev2.Bridge,
) *bridgev2.UserLogin {
	ce := &commands.Event{
		Ctx:    ctx,
		Portal: portal,
		User:   user,
		Bridge: br,
	}
	login, err := bridgesdk.ResolveCommandLogin(ctx, ce, defaultLogin)
	if err != nil {
		return nil
	}
	return login
}

func getAIClient(ce *commands.Event) *AIClient {
	if ce == nil || ce.User == nil {
		return nil
	}

	defaultLogin := ce.User.GetDefaultLogin()
	br := ce.Bridge
	if ce.User.Bridge != nil {
		br = ce.User.Bridge
	}

	login := resolveLoginForCommand(ce.Ctx, ce.Portal, ce.User, defaultLogin, br)
	if login == nil {
		return nil
	}
	client, ok := login.Client.(*AIClient)
	if !ok {
		return nil
	}
	return client
}

func hasLoginForCommand(ce *commands.Event) bool {
	return getAIClient(ce) != nil
}

func getPortalMeta(ce *commands.Event) *PortalMetadata {
	if ce.Portal == nil {
		return nil
	}
	return portalMeta(ce.Portal)
}

func isValidAgentID(agentID string) bool {
	if agentID == "" {
		return false
	}
	for i := range len(agentID) {
		ch := agentID[i]
		if (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') && ch != '-' {
			return false
		}
	}
	return true
}

var _ = registerAICommand(commandregistry.Definition{
	Name:           "new",
	Description:    "Create a new chat of the same type (agent or model)",
	Args:           "[agent <agent_id>]",
	Section:        HelpSectionAI,
	RequiresPortal: true,
	RequiresLogin:  true,
	Handler:        fnNew,
})

func fnNew(ce *commands.Event) {
	client, meta, ok := requireClientMeta(ce)
	if !ok {
		return
	}
	if err := client.validateNewChatCommand(ce.Ctx, ce.Portal, meta, ce.Args); err != nil {
		markCommandFailure(ce, err.Error(), event.MessageStatusUnsupported)
		ce.Reply("%s", err.Error())
		return
	}
	go client.handleNewChat(ce.Ctx, nil, ce.Portal, meta, ce.Args)
}
