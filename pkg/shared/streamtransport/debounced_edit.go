package streamtransport

import (
	"context"
	"strings"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/format"
	"maunium.net/go/mautrix/id"
)

// SendFunc sends a message event to a Matrix room. Callers build this from
// the intent they resolve, keeping intent handling out of this package.
type SendFunc func(ctx context.Context, roomID id.RoomID, content *event.Content) error

// DebouncedEditParams holds the inputs needed by SendDebouncedEdit.
type DebouncedEditParams struct {
	Portal         *bridgev2.Portal
	Force          bool
	SuppressSend   bool
	VisibleBody    string
	FallbackBody   string
	InitialEventID id.EventID
	TurnID         string

	SendFunc SendFunc
	Log      *zerolog.Logger
}

// SendDebouncedEdit sends a debounced replace-edit for a streaming message.
// Returns true if an edit was actually sent.
func SendDebouncedEdit(ctx context.Context, p DebouncedEditParams) bool {
	if p.Portal == nil || p.Portal.MXID == "" || p.InitialEventID == "" {
		return false
	}
	if p.SuppressSend {
		return false
	}
	body := strings.TrimSpace(p.VisibleBody)
	if body == "" {
		body = strings.TrimSpace(p.FallbackBody)
	}
	if body == "" {
		return false
	}

	if !p.Force {
		return false
	}

	if p.SendFunc == nil {
		return false
	}
	rendered := format.RenderMarkdown(body, true, true)
	raw := BuildReplaceEditRaw(p.InitialEventID.String(), rendered.Body, rendered.FormattedBody, rendered.Format)
	if raw == nil {
		return false
	}
	if err := p.SendFunc(ctx, p.Portal.MXID, &event.Content{Raw: raw}); err != nil {
		if p.Log != nil {
			p.Log.Warn().Err(err).Stringer("event_id", p.InitialEventID).Msg("Failed to send debounced stream edit")
		}
		return false
	}
	return true
}
