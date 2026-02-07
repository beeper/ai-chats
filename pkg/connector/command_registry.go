package connector

import (
	"strings"

	"maunium.net/go/mautrix/bridgev2/commands"

	"github.com/beeper/ai-bridge/pkg/connector/commandregistry"
)

var aiCommandRegistry = commandregistry.NewRegistry()

func registerAICommand(def commandregistry.Definition) *commands.FullHandler {
	return aiCommandRegistry.Register(def)
}

// registerCommands registers all AI commands with the command processor.
func (oc *OpenAIConnector) registerCommands(proc *commands.Processor) {
	handlers := aiCommandRegistry.All()
	if len(handlers) > 0 {
		commandHandlers := make([]commands.CommandHandler, 0, len(handlers))
		for _, handler := range handlers {
			if handler == nil || handler.Func == nil {
				continue
			}
			original := handler.Func
			handler.Func = func(ce *commands.Event) {
				if ce != nil && ce.Portal != nil && ce.Portal.Metadata != nil {
					if pm, ok := ce.Portal.Metadata.(*PortalMetadata); ok && pm != nil && pm.IsCodexRoom {
						ce.Reply("This is a Codex room. `!ai` commands are not supported here. Use `/status`, `/new`, or `/approve`.")
						return
					}
				}
				senderID := ""
				if ce != nil && ce.User != nil {
					senderID = ce.User.MXID.String()
				}
				if !isOwnerAllowed(&oc.Config, senderID) {
					if ce != nil {
						ce.Reply("That command is restricted to configured owners.")
					}
					return
				}
				original(ce)
			}
			commandHandlers = append(commandHandlers, handler)
		}
		proc.AddHandlers(commandHandlers...)
	}

	names := aiCommandRegistry.Names()
	oc.br.Log.Info().
		Str("section", HelpSectionAI.Name).
		Int("section_order", HelpSectionAI.Order).
		Strs("commands", names).
		Msg("Registered AI commands: " + strings.Join(names, ", "))
}
