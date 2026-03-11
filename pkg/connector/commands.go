package connector

import (
	"context"
	"errors"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/commands"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/agentremote/pkg/connector/commandregistry"
)

// HelpSectionAI is the help section for AI-related commands.
var HelpSectionAI = commands.HelpSection{
	Name:  "AI Chat",
	Order: 30,
}

func resolveLoginForCommand(
	ctx context.Context,
	portal *bridgev2.Portal,
	defaultLogin *bridgev2.UserLogin,
	getByID func(context.Context, networkid.UserLoginID) (*bridgev2.UserLogin, error),
) *bridgev2.UserLogin {
	if portal == nil || portal.Portal == nil || portal.Receiver == "" || getByID == nil {
		return defaultLogin
	}
	login, err := getByID(ctx, portal.Receiver)
	if err == nil && login != nil {
		return login
	}
	return defaultLogin
}

func getAIClient(ce *commands.Event) *AIClient {
	if ce == nil || ce.User == nil {
		return nil
	}

	defaultLogin := ce.User.GetDefaultLogin()
	if connector, ok := ce.Bridge.Network.(*OpenAIConnector); ok && connector != nil {
		defaultLogin = connector.getPreferredUserLogin(ce.Ctx, ce.User)
	}
	br := ce.Bridge
	if ce.User.Bridge != nil {
		br = ce.User.Bridge
	}

	login := resolveLoginForCommand(ce.Ctx, ce.Portal, defaultLogin, func(ctx context.Context, id networkid.UserLoginID) (*bridgev2.UserLogin, error) {
		if br == nil {
			return nil, errors.New("missing bridge")
		}
		return br.GetExistingUserLoginByID(ctx, id)
	})
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
