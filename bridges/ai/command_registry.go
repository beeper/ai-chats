package ai

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/commands"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/event/cmdschema"

	"github.com/beeper/agentremote/bridges/ai/commandregistry"
	integrationruntime "github.com/beeper/agentremote/pkg/integrations/runtime"
)

var aiCommandRegistry = commandregistry.NewRegistry()
var moduleCommandRegisterMu sync.Mutex
var moduleCommandsRegistered = map[string]struct{}{}
var allowedUserCommandNames = map[string]struct{}{
	"agents": {},
	"cron":   {},
	"memory": {},
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
			handlerCopy := *handler
			original := handlerCopy.Func
			handlerCopy.Func = func(ce *commands.Event) {
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
			commandHandlers = append(commandHandlers, &handlerCopy)
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

func (oc *AIClient) BroadcastCommandDescriptions(ctx context.Context, portal *bridgev2.Portal) {
	if oc == nil || oc.UserLogin == nil || oc.UserLogin.Bridge == nil || portal == nil || portal.MXID == "" {
		return
	}
	bot := oc.UserLogin.Bridge.Bot
	if bot == nil {
		return
	}
	for _, handler := range aiCommandRegistry.All() {
		if handler == nil || handler.Name == "" || !isUserFacingCommand(handler.Name) {
			continue
		}
		content := &cmdschema.EventContent{
			Command:     handler.Name,
			Description: event.MakeExtensibleText(commandDescription(handler)),
		}
		_, err := bot.SendState(ctx, portal.MXID, event.StateMSC4391BotCommand, handler.Name, &event.Content{
			Parsed: content,
		}, time.Time{})
		if err != nil {
			oc.loggerForContext(ctx).Warn().Err(err).Str("command", handler.Name).Stringer("room_id", portal.MXID).Msg("Failed to send command description state")
		}
	}
}

func commandDescription(handler *commands.FullHandler) string {
	if handler != nil {
		if description := strings.TrimSpace(handler.Help.Description); description != "" {
			return description
		}
	}
	return "AI command"
}
