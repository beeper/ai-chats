package sdk

import (
	"errors"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/commands"
	"maunium.net/go/mautrix/event"
)

var sdkHelpSection = commands.HelpSection{Name: "SDK", Order: 50}

// registerCommands registers Config.Commands with the bridgev2 command processor.
func registerCommands[SessionT SessionValue, ConfigDataT ConfigValue](br *bridgev2.Bridge, cfg *Config[SessionT, ConfigDataT]) {
	if len(cfg.Commands) == 0 || br == nil {
		return
	}
	proc, ok := br.Commands.(*commands.Processor)
	if !ok {
		return
	}
	var handlers []commands.CommandHandler
	for _, cmd := range cfg.Commands {
		handler := &commands.FullHandler{
			Name: cmd.Name,
			Help: commands.HelpMeta{
				Section:     sdkHelpSection,
				Description: cmd.Description,
				Args:        cmd.Args,
			},
			RequiresPortal: true,
			RequiresLogin:  true,
			Func: func(ce *commands.Event) {
				if ce.Portal == nil || ce.User == nil {
					return
				}
				login, err := ResolveCommandLogin(ce.Ctx, ce, ce.User.GetDefaultLogin())
				if err != nil || login == nil {
					message := "You're not logged in in this portal."
					if err != nil && !errors.Is(err, bridgev2.ErrNotLoggedIn) {
						message = "Failed to resolve the login for this room."
					}
					if ce.MessageStatus != nil {
						ce.MessageStatus.Status = event.MessageStatusFail
						ce.MessageStatus.ErrorReason = event.MessageStatusNoPermission
						ce.MessageStatus.Message = message
						ce.MessageStatus.IsCertain = true
					}
					ce.Reply("%s", message)
					return
				}
				// Resolve the conversationRuntime from the login's NetworkAPI
				// so that command handlers get a fully-configured Conversation
				// with Session(), agent resolution, and Spec() available.
				var runtime conversationRuntime
				if client, ok := login.Client.(conversationRuntime); ok {
					runtime = client
				}
				conv := newConversation(ce.Ctx, ce.Portal, login, bridgev2.EventSender{}, runtime)
				if err := cmd.Handler(conv, ce.RawArgs); err != nil {
					if ce.MessageStatus != nil {
						ce.MessageStatus.Status = event.MessageStatusFail
						ce.MessageStatus.ErrorReason = event.MessageStatusGenericError
						ce.MessageStatus.Message = err.Error()
						ce.MessageStatus.IsCertain = true
					}
					ce.Reply("Command failed: %s", err.Error())
				}
			},
		}
		handlers = append(handlers, handler)
	}
	proc.AddHandlers(handlers...)
}
