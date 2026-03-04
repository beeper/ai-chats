package runtime

import "strings"

// NormalizeInboundTextNewlines converts all line endings to \n.
func NormalizeInboundTextNewlines(input string) string {
	return strings.ReplaceAll(strings.ReplaceAll(input, "\r\n", "\n"), "\r", "\n")
}

// firstNonEmpty returns the first argument that is non-empty after trimming.
func firstNonEmpty(candidates ...string) string {
	for _, c := range candidates {
		if strings.TrimSpace(c) != "" {
			return c
		}
	}
	return ""
}

// FinalizeInboundContext normalizes an InboundContext by trimming fields,
// normalizing newlines, and filling in body fallbacks.
func FinalizeInboundContext(ctx InboundContext) InboundContext {
	ctx.Body = NormalizeInboundTextNewlines(ctx.Body)
	ctx.RawBody = NormalizeInboundTextNewlines(ctx.RawBody)
	ctx.ThreadStarterBody = NormalizeInboundTextNewlines(ctx.ThreadStarterBody)

	if strings.TrimSpace(ctx.BodyForAgent) == "" {
		ctx.BodyForAgent = firstNonEmpty(ctx.Body, ctx.RawBody)
	} else {
		ctx.BodyForAgent = NormalizeInboundTextNewlines(ctx.BodyForAgent)
	}

	if strings.TrimSpace(ctx.BodyForCommands) == "" {
		ctx.BodyForCommands = firstNonEmpty(ctx.RawBody, ctx.Body)
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
