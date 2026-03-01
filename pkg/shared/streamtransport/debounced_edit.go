package streamtransport

import (
	"context"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/format"
	"maunium.net/go/mautrix/id"
)

// DebouncedEditParams holds the inputs needed by SendDebouncedEdit.
type DebouncedEditParams struct {
	Portal         *bridgev2.Portal
	Force          bool
	SuppressSend   bool
	VisibleBody    string
	FallbackBody   string
	InitialEventID id.EventID
	TurnID         string

	Gate     **EditDebounceGate
	Debounce time.Duration
	Intent   bridgev2.MatrixAPI
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

	now := time.Now()
	shouldEmit := p.Force
	if !shouldEmit {
		if *p.Gate == nil {
			*p.Gate = NewEditDebounceGate()
		}
		shouldEmit = (*p.Gate).ShouldEmit(p.TurnID, body, now, p.Debounce)
	}
	if !shouldEmit {
		return false
	}

	if p.Intent == nil {
		return false
	}
	rendered := format.RenderMarkdown(body, true, true)
	raw := BuildReplaceEditRaw(p.InitialEventID.String(), rendered.Body, rendered.FormattedBody, rendered.Format)
	if raw == nil {
		return false
	}
	if _, err := p.Intent.SendMessage(ctx, p.Portal.MXID, event.EventMessage, &event.Content{Raw: raw}, nil); err != nil {
		p.Log.Warn().Err(err).Stringer("event_id", p.InitialEventID).Msg("Failed to send debounced stream edit")
	}
	return true
}
