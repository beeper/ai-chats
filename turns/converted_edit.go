package turns

import (
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

// RenderedMarkdownContent holds pre-rendered markdown for building converted edits.
type RenderedMarkdownContent struct {
	Body          string
	Format        event.Format
	FormattedBody string
}

// BuildRenderedConvertedEdit wraps rendered markdown into a standard Matrix edit.
func BuildRenderedConvertedEdit(rendered RenderedMarkdownContent, topLevelExtra map[string]any) *bridgev2.ConvertedEdit {
	return BuildConvertedEdit(&event.MessageEventContent{
		MsgType:       event.MsgText,
		Body:          rendered.Body,
		Format:        rendered.Format,
		FormattedBody: rendered.FormattedBody,
	}, topLevelExtra)
}
