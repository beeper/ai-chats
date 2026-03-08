package streamtransport

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/ai-bridge/pkg/matrixevents"
)

const (
	// Fixed debounce interval for fallback post+edit streaming.
	debounceInterval = 200 * time.Millisecond
	// Retry once for non-fallback ephemeral send errors.
	nonFallbackRetryCount = 1
	// Max wait for debounced worker shutdown and final flush.
	endWaitTimeout = 3 * time.Second
)

type EndReason string

const (
	EndReasonFinish     EndReason = "finish"
	EndReasonDisconnect EndReason = "disconnect"
	EndReasonError      EndReason = "error"
)

type StreamSessionParams struct {
	TurnID  string
	AgentID string

	GetTargetEventID func() string
	GetRoomID        func() id.RoomID
	GetSuppressSend  func() bool
	NextSeq          func() int

	RuntimeFallbackFlag *atomic.Bool
	GetEphemeralSender  func(ctx context.Context) (bridgev2.EphemeralSendingMatrixAPI, bool)
	SendDebouncedEdit   func(ctx context.Context, force bool) error
	ClearTurnGate       func()
	SendHook            func(turnID string, seq int, content map[string]any, txnID string) bool
	Logger              *zerolog.Logger
}

type debounceRequest struct {
	force bool
}

type StreamSession struct {
	params StreamSessionParams

	closed atomic.Bool
	// Local fallback for cases where RuntimeFallbackFlag is nil.
	localFallback atomic.Bool

	sendCtx    context.Context
	sendCancel context.CancelFunc

	debounceReqCh chan debounceRequest
	workerStopCh  chan struct{}
	workerDoneCh  chan struct{}
	endWorker     func() // closes workerStopCh exactly once

	// Lazy worker start: goroutine and channels are only allocated when needed.
	ensureWorker  func() // lazily starts the debounce worker goroutine
	workerStarted atomic.Bool
}

func NewStreamSession(params StreamSessionParams) *StreamSession {
	sendCtx, sendCancel := context.WithCancel(context.Background())
	s := &StreamSession{
		params:       params,
		sendCtx:      sendCtx,
		sendCancel:   sendCancel,
		workerStopCh: make(chan struct{}),
		workerDoneCh: make(chan struct{}),
	}
	s.endWorker = sync.OnceFunc(func() {
		close(s.workerStopCh)
	})
	s.ensureWorker = sync.OnceFunc(func() {
		s.debounceReqCh = make(chan debounceRequest, 256)
		s.workerStarted.Store(true)
		go s.runDebouncedWorker()
	})
	return s
}

func (s *StreamSession) IsClosed() bool {
	return s == nil || s.closed.Load()
}

func (s *StreamSession) End(ctx context.Context, _ EndReason) {
	if s == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if !s.closed.CompareAndSwap(false, true) {
		return
	}
	s.sendCancel()
	if s.workerStarted.Load() {
		s.endWorker()
		waitCtx, cancel := context.WithTimeout(ctx, endWaitTimeout)
		defer cancel()
		select {
		case <-s.workerDoneCh:
		case <-waitCtx.Done():
		}
	}
	if s.params.ClearTurnGate != nil {
		s.params.ClearTurnGate()
	}
}

func (s *StreamSession) EmitPart(ctx context.Context, part map[string]any) {
	if s.IsClosed() {
		return
	}
	if part == nil {
		return
	}
	if s.params.GetSuppressSend != nil && s.params.GetSuppressSend() {
		return
	}

	partType, _ := part["type"].(string)
	partType = strings.TrimSpace(partType)
	debounceEligible, forceDebounced := debouncedPartMode(partType)

	turnID := strings.TrimSpace(s.params.TurnID)
	if turnID == "" {
		return
	}
	if s.useDebouncedMode() {
		if debounceEligible {
			s.enqueueDebounced(forceDebounced)
		}
		return
	}
	if s.params.NextSeq == nil {
		return
	}

	// Build the envelope once and share it between hook and ephemeral paths.
	seq := s.params.NextSeq()
	targetEventID := ""
	if s.params.GetTargetEventID != nil {
		targetEventID = strings.TrimSpace(s.params.GetTargetEventID())
	}
	content, err := matrixevents.BuildStreamEventEnvelope(turnID, seq, part, matrixevents.StreamEventOpts{
		TargetEventID: targetEventID,
		AgentID:       strings.TrimSpace(s.params.AgentID),
	})
	if err != nil {
		if s.params.Logger != nil {
			s.params.Logger.Warn().Err(err).Msg("Failed to build stream event envelope")
		}
		return
	}
	txnID := matrixevents.BuildStreamEventTxnID(turnID, seq)

	// Try hook first; if it handles the event we're done.
	if s.params.SendHook != nil && s.params.SendHook(turnID, seq, content, txnID) {
		return
	}

	if s.params.GetEphemeralSender == nil {
		s.switchToDebounced(ctx, "missing_ephemeral_sender_getter", nil)
		if debounceEligible {
			s.enqueueDebounced(forceDebounced)
		}
		return
	}
	ephemeralSender, ok := s.params.GetEphemeralSender(ctx)
	if !ok || ephemeralSender == nil {
		s.switchToDebounced(ctx, "missing_ephemeral_sender", nil)
		if debounceEligible {
			s.enqueueDebounced(forceDebounced)
		}
		return
	}
	eventContent := &event.Content{Raw: content}
	_ = s.sendEphemeralWithRetry(ephemeralSender, eventContent, txnID, partType)
}

func (s *StreamSession) sendEphemeralWithRetry(ephemeralSender bridgev2.EphemeralSendingMatrixAPI, eventContent *event.Content, txnID string, partType string) bool {
	if s.IsClosed() || ephemeralSender == nil || eventContent == nil {
		return false
	}
	send := func() error {
		if s.IsClosed() {
			return context.Canceled
		}
		roomID := id.RoomID("")
		if s.params.GetRoomID != nil {
			roomID = s.params.GetRoomID()
		}
		if roomID == "" {
			return context.Canceled
		}
		_, err := ephemeralSender.BeeperSendEphemeralEvent(s.sendCtx, roomID, matrixevents.StreamEventMessageType, eventContent, txnID)
		return err
	}
	err := send()
	if err == nil {
		return true
	}
	if ShouldFallbackToDebounced(err) {
		s.switchToDebounced(context.Background(), "ephemeral_send_unknown", err)
		if eligible, force := debouncedPartMode(partType); eligible {
			s.enqueueDebounced(force)
		}
		return false
	}
	for i := 0; i < nonFallbackRetryCount; i++ {
		if s.IsClosed() {
			return false
		}
		retryErr := send()
		if retryErr == nil {
			return true
		}
		err = retryErr
		if ShouldFallbackToDebounced(err) {
			s.switchToDebounced(context.Background(), "ephemeral_send_unknown_retry", err)
			if eligible, force := debouncedPartMode(partType); eligible {
				s.enqueueDebounced(force)
			}
			return false
		}
	}
	s.logWarn("ephemeral_send_failed", err)
	return false
}

func (s *StreamSession) useDebouncedMode() bool {
	if s == nil {
		return true
	}
	if s.localFallback.Load() {
		return true
	}
	return s.params.RuntimeFallbackFlag != nil && s.params.RuntimeFallbackFlag.Load()
}

func (s *StreamSession) switchToDebounced(_ context.Context, reason string, err error) {
	if s == nil {
		return
	}
	switched := false
	if s.params.RuntimeFallbackFlag != nil {
		switched = s.params.RuntimeFallbackFlag.CompareAndSwap(false, true)
	} else {
		switched = s.localFallback.CompareAndSwap(false, true)
	}
	if !switched {
		return
	}
	s.logWarn(reason, err)
}

func (s *StreamSession) enqueueDebounced(force bool) {
	if s.IsClosed() {
		return
	}
	s.ensureWorker()
	req := debounceRequest{force: force}
	select {
	case s.debounceReqCh <- req:
	case <-s.workerStopCh:
	}
}

func (s *StreamSession) runDebouncedWorker() {
	defer close(s.workerDoneCh)

	var timer *time.Timer
	var timerCh <-chan time.Time
	pending := false

	stopTimer := func() {
		if timer == nil {
			return
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer = nil
		timerCh = nil
	}

	for {
		select {
		case <-s.workerStopCh:
			stopTimer()
			if pending {
				_ = s.sendDebounced(context.Background(), true)
				if s.params.ClearTurnGate != nil {
					s.params.ClearTurnGate()
				}
			}
			return
		case req := <-s.debounceReqCh:
			if req.force {
				stopTimer()
				pending = false
				_ = s.sendDebounced(context.Background(), true)
				if s.params.ClearTurnGate != nil {
					s.params.ClearTurnGate()
				}
				continue
			}
			pending = true
			if timer == nil {
				timer = time.NewTimer(debounceInterval)
				timerCh = timer.C
			}
		case <-timerCh:
			stopTimer()
			pending = false
			_ = s.sendDebounced(context.Background(), false)
		}
	}
}

func (s *StreamSession) sendDebounced(ctx context.Context, force bool) error {
	if s == nil {
		return context.Canceled
	}
	if s.params.SendDebouncedEdit == nil {
		return nil
	}
	return s.params.SendDebouncedEdit(ctx, force)
}

func debouncedPartMode(partType string) (eligible bool, force bool) {
	switch partType {
	case "text-delta", "reasoning-delta", "text-end", "reasoning-end":
		return true, false
	case "finish", "abort", "error":
		return true, true
	default:
		return false, false
	}
}

func (s *StreamSession) logWarn(reason string, err error) {
	if s == nil || s.params.Logger == nil {
		return
	}
	ev := s.params.Logger.Warn().Str("reason", reason)
	if err != nil {
		ev = ev.Err(err)
	}
	ev.Msg("Switching stream transport to debounced_edit for this runtime; ephemeral streaming will be retried after bridge restart")
}
