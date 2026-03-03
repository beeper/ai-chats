package connector

import (
	"context"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/ai-bridge/pkg/shared/streamtransport"
)

func (oc *AIClient) sendDebouncedStreamEdit(ctx context.Context, portal *bridgev2.Portal, state *streamingState, force bool) error {
	if oc == nil || state == nil || portal == nil {
		return nil
	}
	content := streamtransport.BuildDebouncedEditContent(streamtransport.DebouncedEditParams{
		PortalMXID:   portal.MXID.String(),
		Force:        force,
		SuppressSend: state.suppressSend,
		VisibleBody:  state.visibleAccumulated.String(),
		FallbackBody: state.accumulated.String(),
	})
	if content == nil || state.networkMessageID == "" {
		return nil
	}
	sender := oc.senderForPortal(ctx, portal)
	oc.UserLogin.QueueRemoteEvent(&AIRemoteEdit{
		portal:        portal.PortalKey,
		sender:        sender,
		targetMessage: state.networkMessageID,
		timestamp:     time.Now(),
		preBuilt: &bridgev2.ConvertedEdit{
			ModifiedParts: []*bridgev2.ConvertedEditPart{{
				Type: event.EventMessage,
				Content: &event.MessageEventContent{
					MsgType:       event.MsgText,
					Body:          content.Body,
					Format:        content.Format,
					FormattedBody: content.FormattedBody,
				},
				Extra: map[string]any{"m.mentions": map[string]any{}},
				TopLevelExtra: map[string]any{
					"com.beeper.dont_render_edited": true,
					"m.mentions":                    map[string]any{},
				},
			}},
		},
	})
	return nil
}
