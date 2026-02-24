package codex

import (
	"context"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/format"

	"github.com/beeper/ai-bridge/pkg/shared/streamtransport"
)

func (cc *CodexClient) streamTransportMode() streamtransport.Mode {
	if cc == nil || cc.connector == nil {
		return streamtransport.DefaultMode
	}
	if strings.TrimSpace(cc.connector.Config.Bridge.StreamingTransport) == string(streamtransport.ModeDebouncedEdit) {
		return streamtransport.ModeDebouncedEdit
	}
	return streamtransport.ModeEphemeral
}

func (cc *CodexClient) streamEditDebounceDuration() time.Duration {
	if cc == nil || cc.connector == nil {
		return time.Duration(streamtransport.DefaultEditDebounceMs) * time.Millisecond
	}
	ms := cc.connector.Config.Bridge.StreamingDebounce
	if ms <= 0 {
		ms = streamtransport.DefaultEditDebounceMs
	}
	return time.Duration(ms) * time.Millisecond
}

func (cc *CodexClient) sendDebouncedStreamEdit(ctx context.Context, portal *bridgev2.Portal, state *streamingState, force bool) {
	if cc == nil || portal == nil || state == nil || portal.MXID == "" || state.initialEventID == "" {
		return
	}
	if state.suppressSend {
		return
	}
	body := strings.TrimSpace(state.visibleAccumulated.String())
	if body == "" {
		body = strings.TrimSpace(state.accumulated.String())
	}
	if body == "" {
		return
	}

	now := time.Now()
	shouldEmit := force
	if !shouldEmit {
		if cc.streamEditGate == nil {
			cc.streamEditGate = streamtransport.NewEditDebounceGate()
		}
		shouldEmit = cc.streamEditGate.ShouldEmit(state.turnID, body, now, cc.streamEditDebounceDuration())
	}
	if !shouldEmit {
		return
	}

	intent := cc.getCodexIntent(ctx, portal)
	if intent == nil {
		return
	}
	rendered := format.RenderMarkdown(body, true, true)
	raw := streamtransport.BuildReplaceEditRaw(state.initialEventID.String(), rendered.Body, rendered.FormattedBody, rendered.Format)
	if raw == nil {
		return
	}
	if _, err := intent.SendMessage(ctx, portal.MXID, event.EventMessage, &event.Content{Raw: raw}, nil); err != nil {
		cc.loggerForContext(ctx).Warn().Err(err).Stringer("event_id", state.initialEventID).Msg("Failed to send debounced stream edit")
	}
}
