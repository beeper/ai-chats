package turns

import (
	"strings"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/format"
)

// DebouncedEditParams holds the inputs needed by BuildDebouncedEditContent.
type DebouncedEditParams struct {
	PortalMXID   string
	Force        bool
	SuppressSend bool
	VisibleBody  string
	FallbackBody string
}

// BuildDebouncedEditContent validates inputs and renders the edit content.
// Returns nil if the edit should be skipped.
func BuildDebouncedEditContent(p DebouncedEditParams) *RenderedMarkdownContent {
	if strings.TrimSpace(p.PortalMXID) == "" || p.SuppressSend {
		return nil
	}
	body := strings.TrimSpace(p.VisibleBody)
	if body == "" {
		body = strings.TrimSpace(p.FallbackBody)
	}
	if body == "" {
		return nil
	}
	rendered := format.RenderMarkdown(body, true, true)
	return &RenderedMarkdownContent{
		Body:          rendered.Body,
		FormattedBody: rendered.FormattedBody,
		Format:        rendered.Format,
	}
}

// BuildConvertedEdit wraps rendered message content into a standard Matrix edit.
// The bridge layer will derive the Matrix edit fallback fields from Content via SetEdit,
// so TopLevelExtra should only contain custom top-level fields.
func BuildConvertedEdit(content *event.MessageEventContent, topLevelExtra map[string]any) *bridgev2.ConvertedEdit {
	if content == nil {
		return nil
	}
	if topLevelExtra == nil {
		topLevelExtra = map[string]any{}
	}
	return &bridgev2.ConvertedEdit{
		ModifiedParts: []*bridgev2.ConvertedEditPart{{
			Type: event.EventMessage,
			Content: &event.MessageEventContent{
				MsgType:       content.MsgType,
				Body:          content.Body,
				Format:        content.Format,
				FormattedBody: content.FormattedBody,
			},
			Extra:         map[string]any{"m.mentions": map[string]any{}},
			TopLevelExtra: topLevelExtra,
		}},
	}
}
