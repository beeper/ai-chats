package connector

import (
	"strings"
	"sync"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2/commands"

	"github.com/beeper/ai-bridge/pkg/connector/commandregistry"
	integrationruntime "github.com/beeper/ai-bridge/pkg/integrations/runtime"
)

var aiCommandRegistry = commandregistry.NewRegistry()
var moduleCommandRegisterMu sync.Mutex
var moduleCommandsRegistered = map[string]struct{}{}

func registerAICommand(def commandregistry.Definition) *commands.FullHandler {
	return aiCommandRegistry.Register(def)
}

func registerModuleCommands(defs []integrationruntime.CommandDefinition) {
	if len(defs) == 0 {
		return
	}
	moduleCommandRegisterMu.Lock()
	defer moduleCommandRegisterMu.Unlock()

	for _, def := range defs {
		name := strings.ToLower(strings.TrimSpace(def.Name))
		if name == "" {
			continue
		}
		if _, exists := moduleCommandsRegistered[name]; exists {
			continue
		}
		moduleCommandsRegistered[name] = struct{}{}

		commandName := name
		adminOnly := def.AdminOnly
		description := def.Description
		if strings.TrimSpace(description) == "" {
			description = "Integration command"
		}
		registerAICommand(commandregistry.Definition{
			Name:           commandName,
			Description:    description,
			Args:           def.Args,
			Aliases:        def.Aliases,
			Section:        HelpSectionAI,
			RequiresPortal: def.RequiresPortal,
			RequiresLogin:  def.RequiresLogin,
			Handler: func(ce *commands.Event) {
				client, meta, ok := requireClientMeta(ce)
				if !ok {
					return
				}
				if adminOnly {
					if ce.User == nil || !ce.User.Permissions.Admin {
						ce.Reply("Only bridge admins can use this command.")
						return
					}
				}
				handled, err := client.executeIntegratedCommand(
					ce.Ctx,
					ce.Portal,
					meta,
					ce,
					commandName,
					ce.Args,
					ce.RawArgs,
					ce.Reply,
				)
				if err != nil {
					ce.Reply("Command failed: %s", err.Error())
					return
				}
				if !handled {
					ce.Reply("Command unavailable.")
				}
			},
		})
	}
}

// registerCommands registers all AI commands with the command processor.
func (oc *OpenAIConnector) registerCommands(proc *commands.Processor) {
	registerCommandsWithOwnerGuard(proc, &oc.Config, &oc.br.Log, HelpSectionAI)
}

func registerCommandsWithOwnerGuard(proc *commands.Processor, cfg *Config, log *zerolog.Logger, section commands.HelpSection) {
	handlers := aiCommandRegistry.All()
	if len(handlers) > 0 {
		commandHandlers := make([]commands.CommandHandler, 0, len(handlers))
		for _, handler := range handlers {
			if handler == nil || handler.Func == nil {
				continue
			}
			original := handler.Func
			handler.Func = func(ce *commands.Event) {
				senderID := ""
				if ce != nil && ce.User != nil {
					senderID = ce.User.MXID.String()
				}
				if !isOwnerAllowed(cfg, senderID) {
					if ce != nil {
						ce.Reply("Only configured owners can use that command.")
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
	log.Info().
		Str("section", section.Name).
		Int("section_order", section.Order).
		Strs("commands", names).
		Msg("Registered AI commands: " + strings.Join(names, ", "))
}
