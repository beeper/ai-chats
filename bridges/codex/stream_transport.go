package codex

import (
	"context"
	"time"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/ai-bridge/pkg/shared/streamtransport"
)

func (cc *CodexClient) streamTransportMode() streamtransport.Mode {
	if cc == nil || cc.connector == nil {
		return streamtransport.DefaultMode
	}
	return streamtransport.ResolveMode(cc.connector.Config.Bridge.StreamingTransport)
}

func (cc *CodexClient) streamEditDebounceDuration() time.Duration {
	if cc == nil || cc.connector == nil {
		return streamtransport.ResolveDebounceDuration(0)
	}
	return streamtransport.ResolveDebounceDuration(cc.connector.Config.Bridge.StreamingDebounce)
}

func (cc *CodexClient) sendDebouncedStreamEdit(ctx context.Context, portal *bridgev2.Portal, state *streamingState, force bool) {
	if cc == nil || state == nil || portal == nil {
		return
	}
	streamtransport.SendDebouncedEdit(ctx, streamtransport.DebouncedEditParams{
		Portal:         portal,
		Force:          force,
		SuppressSend:   state.suppressSend,
		VisibleBody:    state.visibleAccumulated.String(),
		FallbackBody:   state.accumulated.String(),
		InitialEventID: state.initialEventID,
		TurnID:         state.turnID,
		Gate:           &cc.streamEditGate,
		Debounce:       cc.streamEditDebounceDuration(),
		Intent:         cc.getCodexIntent(ctx, portal),
		Log:            cc.loggerForContext(ctx),
	})
}
