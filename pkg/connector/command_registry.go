package connector

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/commands"
	"maunium.net/go/mautrix/event"

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

// BroadcastCommandDescriptions sends com.beeper.command_description state events
// for all registered AI commands into the given room. This enables clients
// to discover and render slash commands with autocomplete.
func (oc *AIClient) BroadcastCommandDescriptions(ctx context.Context, portal *bridgev2.Portal) {
	if oc == nil || oc.UserLogin == nil || portal == nil || portal.MXID == "" {
		return
	}
	log := oc.loggerForContext(ctx)
	handlers := aiCommandRegistry.All()
	if len(handlers) == 0 {
		return
	}

	bot := oc.UserLogin.Bridge.Bot
	if bot == nil {
		log.Warn().Msg("command_description: no bot intent available to broadcast command descriptions")
		return
	}

	for _, handler := range handlers {
		if handler == nil || handler.Name == "" {
			continue
		}
		description := strings.TrimSpace(handler.Help.Description)
		if description == "" {
			description = "AI command"
		}

		content := map[string]any{
			"description": description,
		}
		// Parse args string into structured arguments map if present
		args := strings.TrimSpace(handler.Help.Args)
		if args != "" {
			content["arguments"] = buildCommandArguments(args)
		}

		stateKey := handler.Name
		_, err := bot.SendState(ctx, portal.MXID, event.StateMSC4391BotCommand, stateKey, &event.Content{
			Raw: content,
		}, time.Time{})
		if err != nil {
			log.Warn().Err(err).Str("command", handler.Name).Msg("command_description: failed to send state event")
		}
	}
	log.Debug().Int("count", len(handlers)).Stringer("room", portal.MXID).Msg("command_description: broadcast command descriptions")
}

// buildCommandArguments converts a simple args string like "<model_id> [reason]"
// into a structured arguments map for com.beeper.command_description.
func buildCommandArguments(argsStr string) map[string]any {
	args := map[string]any{}
	for _, part := range strings.Fields(argsStr) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		required := false
		name := part
		if strings.HasPrefix(name, "<") && strings.HasSuffix(name, ">") {
			name = name[1 : len(name)-1]
			required = true
		} else if strings.HasPrefix(name, "[") && strings.HasSuffix(name, "]") {
			name = name[1 : len(name)-1]
		}
		// Strip pipes (e.g. "allow|always|deny" → use first as name)
		if idx := strings.Index(name, "|"); idx > 0 {
			name = name[:idx]
		}
		args[name] = map[string]any{
			"description": part,
			"required":    required,
			"type":        "string",
		}
	}
	return args
}
