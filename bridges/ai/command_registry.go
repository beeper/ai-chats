package ai

import (
	"context"
	"strings"
	"sync"
	"unicode"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2/commands"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/agentremote/bridges/ai/commandregistry"
	integrationruntime "github.com/beeper/agentremote/pkg/integrations/runtime"
)

var aiCommandRegistry = commandregistry.NewRegistry()
var moduleCommandRegisterMu sync.Mutex
var moduleCommandsRegistered = map[string]struct{}{}
var allowedUserCommandNames = map[string]struct{}{
	"agents": {},
	"new":    {},
	"reset":  {},
	"status": {},
	"stop":   {},
}

func isUserFacingCommand(name string) bool {
	_, ok := allowedUserCommandNames[strings.TrimSpace(strings.ToLower(name))]
	return ok
}

func markCommandFailure(ce *commands.Event, message string, reason event.MessageStatusReason) {
	if ce == nil || ce.MessageStatus == nil {
		return
	}
	ce.MessageStatus.Status = event.MessageStatusFail
	ce.MessageStatus.ErrorReason = reason
	ce.MessageStatus.Message = strings.TrimSpace(message)
	ce.MessageStatus.IsCertain = true
}

func registerAICommand(def commandregistry.Definition) *commands.FullHandler {
	if def.RequiresLogin && def.HasLogin == nil {
		def.HasLogin = hasLoginForCommand
	}
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
						markCommandFailure(ce, "Only bridge admins can use this command.", event.MessageStatusNoPermission)
						ce.Reply("Only bridge admins can use this command.")
						return
					}
				}
				handled, err := client.executeIntegratedCommand(
					ce.Ctx,
					ce.Portal,
					meta,
					commandName,
					ce.Args,
					ce.RawArgs,
					ce.Reply,
				)
				if err != nil {
					markCommandFailure(ce, "Command failed: "+err.Error(), event.MessageStatusGenericError)
					ce.Reply("Command failed: %s", err.Error())
					return
				}
				if !handled {
					markCommandFailure(ce, "Command unavailable.", event.MessageStatusUnsupported)
					ce.Reply("Command unavailable.")
				}
			},
		})
	}
}

func registerCommandsWithOwnerGuard(proc *commands.Processor, cfg *Config, log *zerolog.Logger, section commands.HelpSection) {
	handlers := aiCommandRegistry.All()
	if len(handlers) > 0 {
		commandHandlers := make([]commands.CommandHandler, 0, len(handlers))
		for _, handler := range handlers {
			if handler == nil || handler.Func == nil {
				continue
			}
			if !isUserFacingCommand(handler.Name) {
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
						markCommandFailure(ce, "Only configured owners can use that command.", event.MessageStatusNoPermission)
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
