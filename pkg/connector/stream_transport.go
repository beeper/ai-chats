package connector

import (
	"context"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/format"

	"github.com/beeper/ai-bridge/pkg/shared/streamtransport"
)

func (oc *AIClient) streamTransportMode() streamtransport.Mode {
	if oc == nil || oc.connector == nil {
		return streamtransport.DefaultMode
	}
	if strings.TrimSpace(oc.connector.Config.Bridge.StreamingTransport) == string(streamtransport.ModeDebouncedEdit) {
		return streamtransport.ModeDebouncedEdit
	}
	return streamtransport.ModeEphemeral
}

func (oc *AIClient) streamEditDebounceDuration() time.Duration {
	if oc == nil || oc.connector == nil {
		return time.Duration(streamtransport.DefaultEditDebounceMs) * time.Millisecond
	}
	ms := oc.connector.Config.Bridge.StreamingDebounce
	if ms <= 0 {
		ms = streamtransport.DefaultEditDebounceMs
	}
	return time.Duration(ms) * time.Millisecond
}

func (oc *AIClient) sendDebouncedStreamEdit(ctx context.Context, portal *bridgev2.Portal, state *streamingState, force bool) {
	if oc == nil || portal == nil || state == nil || portal.MXID == "" || state.initialEventID == "" {
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
		if oc.streamEditGate == nil {
			oc.streamEditGate = streamtransport.NewEditDebounceGate()
		}
		shouldEmit = oc.streamEditGate.ShouldEmit(state.turnID, body, now, oc.streamEditDebounceDuration())
	}
	if !shouldEmit {
		return
	}

	intent := oc.getModelIntent(ctx, portal)
	if intent == nil {
		return
	}
	rendered := format.RenderMarkdown(body, true, true)
	raw := streamtransport.BuildReplaceEditRaw(state.initialEventID.String(), rendered.Body, rendered.FormattedBody, rendered.Format)
	if raw == nil {
		return
	}
	if _, err := intent.SendMessage(ctx, portal.MXID, event.EventMessage, &event.Content{Raw: raw}, nil); err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Stringer("event_id", state.initialEventID).Msg("Failed to send debounced stream edit")
	}
}
