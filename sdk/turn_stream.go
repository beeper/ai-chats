package sdk

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/pkg/matrixevents"
	"github.com/beeper/agentremote/pkg/shared/streamui"
	"github.com/beeper/agentremote/turns"
)

func (t *Turn) ensureSession() {
	t.sessionOnce.Do(func() {
		var logger zerolog.Logger
		if t.conv != nil && t.conv.login != nil {
			logger = t.conv.login.Log.With().Str("component", "sdk_turn").Logger()
		}
		sender := t.resolveSender(t.turnCtx)

		t.session = turns.NewStreamSession(turns.StreamSessionParams{
			TurnID:  t.turnID,
			AgentID: strings.TrimSpace(string(sender.Sender)),
			GetStreamTarget: func() turns.StreamTarget {
				return turns.StreamTarget{NetworkMessageID: t.NetworkMessageID()}
			},
			ResolveTargetEventID: func(callCtx context.Context, target turns.StreamTarget) (id.EventID, error) {
				if t.conv == nil || t.conv.login == nil || t.conv.login.Bridge == nil {
					return "", nil
				}
				receiver := t.conv.portal.Receiver
				if receiver == "" {
					receiver = t.conv.login.ID
				}
				return turns.ResolveTargetEventIDFromDB(callCtx, t.conv.login.Bridge, receiver, target)
			},
			GetRoomID: func() id.RoomID {
				if t.conv == nil || t.conv.portal == nil {
					return ""
				}
				return t.conv.portal.MXID
			},
			GetTargetEventID: func() id.EventID { return t.InitialEventID() },
			GetSuppressSend:  t.SuppressSend,
			GetStreamType: func() string {
				return matrixevents.StreamEventMessageType.Type
			},
			NextSeq: t.nextSeq,
			GetStreamPublisher: func(callCtx context.Context) (bridgev2.BeeperStreamPublisher, bool) {
				return t.defaultStreamPublisher(callCtx)
			},
			SendHook: t.streamHook,
			Logger:   &logger,
		})
	})
}
func (t *Turn) nextSeq() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.state.InitMaps()
	t.state.UIStepCount++
	return t.state.UIStepCount
}
func (t *Turn) defaultStreamPublisher(callCtx context.Context) (bridgev2.BeeperStreamPublisher, bool) {
	if t.streamPublisherFunc != nil {
		return t.streamPublisherFunc(callCtx)
	}
	if t.conv == nil || t.conv.login == nil || t.conv.login.Bridge == nil || t.conv.login.Bridge.Matrix == nil {
		return nil, false
	}
	publisher := t.conv.login.Bridge.GetBeeperStreamPublisher()
	if publisher == nil {
		return nil, false
	}
	return publisher, true
}

// SetStreamTransport overrides the stream delivery mechanism. The provided
// function is called for every emitted part instead of the default session-
// based transport. UIState tracking (ApplyChunk) is still handled automatically.
func (t *Turn) SetStreamTransport(fn func(ctx context.Context, portal *bridgev2.Portal, part map[string]any)) {
	if fn == nil {
		return
	}
	t.emitter.Emit = func(callCtx context.Context, portal *bridgev2.Portal, part map[string]any) {
		t.emitPart(callCtx, portal, part, func() {
			fn(callCtx, portal, part)
		})
	}
}

// SetStreamPublisherFunc overrides how the Turn resolves the shared stream publisher.
func (t *Turn) SetStreamPublisherFunc(fn func(ctx context.Context) (bridgev2.BeeperStreamPublisher, bool)) {
	t.streamPublisherFunc = fn
}

// StreamDescriptor returns the com.beeper.stream descriptor for the turn's placeholder message.
func (t *Turn) StreamDescriptor(ctx context.Context) (*event.BeeperStreamInfo, error) {
	t.ensureSession()
	if t.session == nil {
		return nil, context.Canceled
	}
	return t.session.Descriptor(ctx)
}
func (t *Turn) emitPart(callCtx context.Context, _ *bridgev2.Portal, part map[string]any, deliver func()) {
	if part == nil {
		return
	}
	t.ensureStarted()
	t.resetIdleTimeout()
	streamui.ApplyChunk(t.state, part)
	if deliver != nil {
		deliver()
	}
}
func (t *Turn) resolvedIdleTimeout() time.Duration {
	const defaultIdleTimeout = time.Minute
	if t == nil || t.conv == nil || t.conv.turnConfig == nil {
		return defaultIdleTimeout
	}
	timeoutMs := t.conv.turnConfig.IdleTimeoutMs
	switch {
	case timeoutMs < 0:
		return 0
	case timeoutMs == 0:
		return defaultIdleTimeout
	default:
		return time.Duration(timeoutMs) * time.Millisecond
	}
}
func (t *Turn) resetIdleTimeout() {
	timeout := t.resolvedIdleTimeout()
	if timeout <= 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.started || t.ended {
		return
	}
	if t.idleTimer != nil {
		t.idleTimer.Stop()
	}
	t.idleTimerSeq++
	seq := t.idleTimerSeq
	t.idleTimer = time.AfterFunc(timeout, func() {
		t.handleIdleTimeout(seq)
	})
}
func (t *Turn) stopIdleTimeout() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.idleTimerSeq++
	if t.idleTimer != nil {
		t.idleTimer.Stop()
		t.idleTimer = nil
	}
}
func (t *Turn) handleIdleTimeout(seq uint64) {
	t.mu.Lock()
	if !t.started || t.ended || t.idleTimerSeq != seq {
		t.mu.Unlock()
		return
	}
	t.mu.Unlock()
	t.Abort("timeout")
}
func (t *Turn) ensureStreamStartedAsync() {
	if t == nil || t.session == nil {
		return
	}
	t.streamStartOnce.Do(func() {
		go t.awaitStreamStart()
	})
}
func (t *Turn) awaitStreamStart() {
	if t == nil || t.session == nil {
		return
	}
	ticker := time.NewTicker(15 * time.Millisecond)
	defer ticker.Stop()

	for {
		started, err := t.session.EnsureStarted(t.turnCtx)
		if err == nil && started {
			return
		}
		if err != nil && (errors.Is(err, turns.ErrClosed) ||
			errors.Is(err, turns.ErrNoPublisher) ||
			errors.Is(err, turns.ErrNoRoomID) ||
			errors.Is(err, turns.ErrNoTargetEventID) ||
			errors.Is(err, context.Canceled)) {
			return
		}
		select {
		case <-t.turnCtx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (t *Turn) flushPendingStream(ctx context.Context) {
	if t == nil || t.session == nil {
		return
	}
	if err := t.session.FlushPending(ctx); err != nil && t.startErr == nil {
		t.startErr = err
	}
}
func (t *Turn) finalizationContext() context.Context {
	if t == nil {
		return context.Background()
	}
	if t.turnCtx != nil && t.turnCtx.Err() == nil {
		return t.turnCtx
	}
	if t.conv != nil && t.conv.ctx != nil && t.conv.ctx.Err() == nil {
		return t.conv.ctx
	}
	if t.ctx != nil && t.ctx.Err() == nil {
		return t.ctx
	}
	if t.conv != nil && t.conv.login != nil && t.conv.login.Bridge != nil && t.conv.login.Bridge.BackgroundCtx != nil {
		return t.conv.login.Bridge.BackgroundCtx
	}
	return context.Background()
}
