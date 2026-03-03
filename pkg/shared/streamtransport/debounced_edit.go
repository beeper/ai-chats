package streamtransport

import (
	"strings"

	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/format"
)

// DebouncedEditContent is the rendered content for a debounced streaming edit.
type DebouncedEditContent struct {
	Body          string
	FormattedBody string
	Format        event.Format
}

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
func BuildDebouncedEditContent(p DebouncedEditParams) *DebouncedEditContent {
	if strings.TrimSpace(p.PortalMXID) == "" {
		return nil
	}
	if p.SuppressSend {
		return nil
	}
	body := strings.TrimSpace(p.VisibleBody)
	if body == "" {
		body = strings.TrimSpace(p.FallbackBody)
	}
	if body == "" {
		return nil
	}
	if !p.Force {
		return nil
	}
	rendered := format.RenderMarkdown(body, true, true)
	return &DebouncedEditContent{
		Body:          rendered.Body,
		FormattedBody: rendered.FormattedBody,
		Format:        rendered.Format,
	}
}
