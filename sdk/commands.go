package sdk

import (
	"context"
	"errors"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/commands"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/event/cmdschema"
)

var sdkHelpSection = commands.HelpSection{Name: "SDK", Order: 50}

// registerCommands registers Config.Commands with the bridgev2 command processor.
func registerCommands(br *bridgev2.Bridge, cfg *Config) {
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

// BroadcastCommandDescriptions sends MSC4391 command-description state events
// for all SDK commands into the given room.
func BroadcastCommandDescriptions(ctx context.Context, portal *bridgev2.Portal, bot bridgev2.MatrixAPI, cmds []Command) {
	if portal == nil || portal.MXID == "" || bot == nil || len(cmds) == 0 {
		return
	}
	for _, cmd := range cmds {
		content := &cmdschema.EventContent{
			Command:     cmd.Name,
			Description: event.MakeExtensibleText(cmd.Description),
		}
		if cmd.Args != "" {
			content.Parameters, content.TailParam = buildSDKCommandParameters(cmd.Args)
		}
		_, _ = bot.SendState(ctx, portal.MXID, event.StateMSC4391BotCommand, cmd.Name, &event.Content{
			Parsed: content,
		}, time.Time{})
	}
}

func buildSDKCommandParameters(argsStr string) ([]*cmdschema.Parameter, string) {
	var params []*cmdschema.Parameter
	var tailParam string
	for _, token := range tokenizeSDKArgs(argsStr) {
		required, name := parseSDKArg(token)
		if name == "" {
			continue
		}
		isTail := strings.Contains(name, "...")
		key := strings.TrimSpace(strings.Trim(strings.ReplaceAll(name, "...", ""), "_"))
		if key == "" {
			key = "args"
		}
		params = append(params, &cmdschema.Parameter{
			Key:         key,
			Schema:      cmdschema.PrimitiveTypeString.Schema(),
			Optional:    !required,
			Description: event.MakeExtensibleText(token),
		})
		if isTail && tailParam == "" {
			tailParam = key
		}
	}
	return params, tailParam
}

func parseSDKArg(token string) (required bool, name string) {
	name = strings.TrimSpace(token)
	if strings.HasPrefix(name, "<") && strings.HasSuffix(name, ">") {
		name = name[1 : len(name)-1]
		required = true
	} else if strings.HasPrefix(name, "[") && strings.HasSuffix(name, "]") {
		name = name[1 : len(name)-1]
	}
	return required, strings.TrimSpace(name)
}

func tokenizeSDKArgs(s string) []string {
	var tokens []string
	i := 0
	for i < len(s) {
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
		j := i + 1
		for j < len(s) && s[j] != ' ' && s[j] != '\t' {
			j++
		}
		tokens = append(tokens, s[i:j])
		i = j
	}
	return tokens
}
