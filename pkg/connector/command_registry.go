package connector

import (
	"context"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/commands"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/event/cmdschema"

	"github.com/beeper/ai-bridge/pkg/connector/commandregistry"
	integrationruntime "github.com/beeper/ai-bridge/pkg/integrations/runtime"
)

var aiCommandRegistry = commandregistry.NewRegistry()
var moduleCommandRegisterMu sync.Mutex
var moduleCommandsRegistered = map[string]struct{}{}
var allowedUserCommandNames = map[string]struct{}{
	"new":    {},
	"reset":  {},
	"status": {},
	"stop":   {},
}

func isUserFacingCommand(name string) bool {
	_, ok := allowedUserCommandNames[strings.TrimSpace(strings.ToLower(name))]
	return ok
}

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

// BroadcastCommandDescriptions sends MSC4391 command-description state events
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
		if !isUserFacingCommand(handler.Name) {
			continue
		}
		stateKey := handler.Name
		content := buildCommandDescriptionContent(handler)
		_, err := bot.SendState(ctx, portal.MXID, event.StateMSC4391BotCommand, stateKey, &event.Content{
			Parsed: content,
		}, time.Time{})
		if err != nil {
			log.Warn().Err(err).Str("command", handler.Name).Msg("command_description: failed to send state event")
		}
	}
	log.Debug().Int("count", len(handlers)).Stringer("room", portal.MXID).Msg("command_description: broadcast command descriptions")
}

func buildCommandDescriptionContent(handler *commands.FullHandler) *cmdschema.EventContent {
	description := "AI command"
	if handler != nil {
		if trimmed := strings.TrimSpace(handler.Help.Description); trimmed != "" {
			description = trimmed
		}
	}
	content := &cmdschema.EventContent{
		Command:     handler.Name,
		Description: event.MakeExtensibleText(description),
	}
	content.Parameters, content.TailParam = buildCommandParameters(handler.Help.Args)
	return content
}

// buildCommandParameters converts a simple args string like "<model_id> [reason]"
// into MSC4391 parameter definitions.
func buildCommandParameters(argsStr string) ([]*cmdschema.Parameter, string) {
	var (
		params    []*cmdschema.Parameter
		tailParam string
	)
	for _, part := range tokenizeArgs(argsStr) {
		required, name := parseCommandArgumentToken(part)
		if name == "" {
			continue
		}
		schema, key, isTail := buildCommandParameterSchema(name)
		if schema == nil || key == "" {
			continue
		}
		params = append(params, &cmdschema.Parameter{
			Key:         key,
			Schema:      schema,
			Optional:    !required,
			Description: event.MakeExtensibleText(part),
		})
		if isTail && tailParam == "" {
			tailParam = key
		}
	}
	return params, tailParam
}

func parseCommandArgumentToken(token string) (required bool, name string) {
	name = strings.TrimSpace(token)
	if strings.HasPrefix(name, "<") && strings.HasSuffix(name, ">") {
		name = name[1 : len(name)-1]
		required = true
	} else if strings.HasPrefix(name, "[") && strings.HasSuffix(name, "]") {
		name = name[1 : len(name)-1]
	}
	return required, strings.TrimSpace(name)
}

func buildCommandParameterSchema(name string) (*cmdschema.ParameterSchema, string, bool) {
	isTail := strings.Contains(name, "...")
	cleanName := strings.TrimSpace(strings.Trim(strings.ReplaceAll(name, "...", ""), "_"))
	keySource := cleanName
	if strings.Contains(cleanName, "|") {
		keySource = strings.TrimSpace(strings.Split(cleanName, "|")[0])
	}
	key := normalizeCommandParameterKey(keySource)
	if key == "" {
		key = "args"
	}

	if strings.Contains(cleanName, "|") {
		options := strings.Split(cleanName, "|")
		var variants []*cmdschema.ParameterSchema
		for _, option := range options {
			option = strings.TrimSpace(option)
			if option == "" {
				continue
			}
			variants = append(variants, cmdschema.Literal(option))
		}
		if len(variants) > 0 {
			return cmdschema.Union(variants...), key, isTail
		}
	}
	return cmdschema.PrimitiveTypeString.Schema(), key, isTail
}

func normalizeCommandParameterKey(name string) string {
	var b strings.Builder
	lastUnderscore := false
	for _, r := range name {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			b.WriteRune(unicode.ToLower(r))
			lastUnderscore = false
		case r == '_' || r == '-' || unicode.IsSpace(r):
			if b.Len() == 0 || lastUnderscore {
				continue
			}
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	return strings.Trim(b.String(), "_")
}

// tokenizeArgs splits an args string into tokens, keeping bracketed segments
// (e.g. "[_model name_]" or "<add|remove>") as single tokens.
func tokenizeArgs(s string) []string {
	var tokens []string
	i := 0
	for i < len(s) {
		// Skip whitespace
		if s[i] == ' ' || s[i] == '\t' {
			i++
			continue
		}
		var close byte
		switch s[i] {
		case '<':
			close = '>'
		case '[':
			close = ']'
		}
		if close != 0 {
			end := strings.IndexByte(s[i+1:], close)
			if end >= 0 {
				tokens = append(tokens, s[i:i+1+end+1])
				i += 1 + end + 1
				continue
			}
		}
		// Plain word: read until next whitespace
		j := i + 1
		for j < len(s) && s[j] != ' ' && s[j] != '\t' {
			j++
		}
		tokens = append(tokens, s[i:j])
		i = j
	}
	return tokens
}
