package streamtransport

import (
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

type RenderedMarkdownContent struct {
	Body          string
	Format        event.Format
	FormattedBody string
}

func BuildRenderedConvertedEdit(rendered RenderedMarkdownContent, topLevelExtra map[string]any) *bridgev2.ConvertedEdit {
	return BuildConvertedEdit(&event.MessageEventContent{
		MsgType:       event.MsgText,
		Body:          rendered.Body,
		Format:        rendered.Format,
		FormattedBody: rendered.FormattedBody,
	}, topLevelExtra)
}
