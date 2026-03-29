package ai

import (
	"context"
	"errors"
	"strings"

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

var _ = registerAICommand(commandregistry.Definition{
	Name:          "agents",
	Description:   "Set whether this login exposes agent chats or model rooms only",
	Args:          "[on|off|status]",
	Section:       HelpSectionAI,
	RequiresLogin: true,
	Handler:       fnAgents,
})

func parseAgentsCommandArgs(args []string, currentlyEnabled bool) (enabled bool, changed bool, reply string, err error) {
	if len(args) == 0 {
		if currentlyEnabled {
			return true, false, "Agents are enabled for this login.", nil
		}
		return false, false, "Agents are disabled for this login.", nil
	}
	if len(args) != 1 {
		return currentlyEnabled, false, "", errInvalidAgentsCommandUsage
	}

	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "status":
		if currentlyEnabled {
			return true, false, "Agents are enabled for this login.", nil
		}
		return false, false, "Agents are disabled for this login.", nil
	case "on", "enable", "enabled", "true":
		return true, !currentlyEnabled, "Agents enabled for this login.", nil
	case "off", "disable", "disabled", "false":
		return false, currentlyEnabled, "Agents disabled for this login. New discovery and chat creation will use model rooms only.", nil
	default:
		return currentlyEnabled, false, "", errInvalidAgentsCommandUsage
	}
}

var errInvalidAgentsCommandUsage = errors.New("usage: !ai agents [on|off|status]")

func fnAgents(ce *commands.Event) {
	client := getAIClient(ce)
	if client == nil || client.UserLogin == nil {
		markCommandFailure(ce, "That command requires you to be logged in.", event.MessageStatusNoPermission)
		ce.Reply("That command requires you to be logged in.")
		return
	}

	loginMeta := loginMetadata(client.UserLogin)
	currentlyEnabled := agentsEnabled(loginMeta)
	enabled, changed, reply, parseErr := parseAgentsCommandArgs(ce.Args, currentlyEnabled)
	if parseErr != nil {
		markCommandFailure(ce, "usage: !ai agents [on|off|status]", event.MessageStatusUnsupported)
		ce.Reply("usage: !ai agents [on|off|status]")
		return
	}

	if changed {
		prev := loginMeta.Agents
		loginMeta.Agents = &enabled
		if err := client.UserLogin.Save(ce.Ctx); err != nil {
			loginMeta.Agents = prev
			markCommandFailure(ce, "Couldn't save AI settings.", event.MessageStatusGenericError)
			ce.Reply("Couldn't save AI settings.")
			return
		}
	}

	ce.Reply("%s", formatSystemAck(reply))
}
