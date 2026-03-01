package runtime

import "strings"

func NormalizeInboundTextNewlines(input string) string {
	return strings.ReplaceAll(strings.ReplaceAll(input, "\r\n", "\n"), "\r", "\n")
}

func FinalizeInboundContext(ctx InboundContext) InboundContext {
	ctx.Body = NormalizeInboundTextNewlines(ctx.Body)
	ctx.RawBody = NormalizeInboundTextNewlines(ctx.RawBody)
	ctx.ThreadStarterBody = NormalizeInboundTextNewlines(ctx.ThreadStarterBody)

	if strings.TrimSpace(ctx.BodyForAgent) == "" {
		switch {
		case strings.TrimSpace(ctx.Body) != "":
			ctx.BodyForAgent = ctx.Body
		case strings.TrimSpace(ctx.RawBody) != "":
			ctx.BodyForAgent = ctx.RawBody
		default:
			ctx.BodyForAgent = ""
		}
	} else {
		ctx.BodyForAgent = NormalizeInboundTextNewlines(ctx.BodyForAgent)
	}

	if strings.TrimSpace(ctx.BodyForCommands) == "" {
		switch {
		case strings.TrimSpace(ctx.RawBody) != "":
			ctx.BodyForCommands = ctx.RawBody
		default:
			ctx.BodyForCommands = ctx.Body
		}
	} else {
		ctx.BodyForCommands = NormalizeInboundTextNewlines(ctx.BodyForCommands)
	}

	ctx.Provider = strings.TrimSpace(ctx.Provider)
	ctx.Surface = strings.TrimSpace(ctx.Surface)
	ctx.ChatType = strings.TrimSpace(strings.ToLower(ctx.ChatType))
	ctx.ChatID = strings.TrimSpace(ctx.ChatID)
	ctx.ConversationLabel = strings.TrimSpace(ctx.ConversationLabel)
	ctx.SenderLabel = strings.TrimSpace(ctx.SenderLabel)
	ctx.SenderID = strings.TrimSpace(ctx.SenderID)
	ctx.MessageID = strings.TrimSpace(ctx.MessageID)
	ctx.MessageIDFull = strings.TrimSpace(ctx.MessageIDFull)
	ctx.ReplyToID = strings.TrimSpace(ctx.ReplyToID)
	ctx.ThreadID = strings.TrimSpace(ctx.ThreadID)

	return ctx
}
