package connector

import (
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/beeper/ai-bridge/pkg/cron"
)

type HeartbeatWakeHandler func(reason string) cron.HeartbeatRunResult

type HeartbeatWake struct {
	mu            sync.Mutex
	handler       HeartbeatWakeHandler
	pendingReason string
	scheduled     bool
	running       bool
	timer         *time.Timer
	log           zerolog.Logger
}

const (
	defaultHeartbeatCoalesce = 250 * time.Millisecond
	defaultHeartbeatRetry    = 1 * time.Second
)

func (w *HeartbeatWake) SetHandler(handler HeartbeatWakeHandler) {
	w.mu.Lock()
	w.handler = handler
	hasPending := w.pendingReason != ""
	w.mu.Unlock()
	if handler != nil && hasPending {
		w.schedule(defaultHeartbeatCoalesce)
	}
}

func (w *HeartbeatWake) Request(reason string, coalesce time.Duration) {
	w.mu.Lock()
	if strings.TrimSpace(reason) == "" {
		if w.pendingReason == "" {
			w.pendingReason = "requested"
		}
	} else {
		w.pendingReason = reason
	}
	w.mu.Unlock()
	w.log.Debug().Str("reason", reason).Dur("coalesce", coalesce).Msg("Heartbeat wake requested")
	if coalesce <= 0 {
		coalesce = defaultHeartbeatCoalesce
	}
	w.schedule(coalesce)
}

func (w *HeartbeatWake) schedule(delay time.Duration) {
	w.mu.Lock()
	if w.timer != nil {
		w.mu.Unlock()
		return
	}
	w.timer = time.AfterFunc(delay, func() {
		w.mu.Lock()
		w.timer = nil
		w.scheduled = false
		handler := w.handler
		if w.running || handler == nil {
			shouldReschedule := w.running
			if shouldReschedule {
				w.scheduled = true
			}
			w.mu.Unlock()
			if shouldReschedule {
				w.schedule(delay)
			}
			return
		}
		reason := w.pendingReason
		w.pendingReason = ""
		w.running = true
		w.mu.Unlock()

		w.log.Debug().Str("reason", reason).Msg("Heartbeat wake executing handler")
		res := cron.HeartbeatRunResult{Status: "skipped", Reason: "disabled"}
		func() {
			defer func() {
				if r := recover(); r != nil {
					w.log.Error().Interface("panic", r).Str("reason", reason).Msg("Heartbeat wake handler panicked")
				}
			}()
			res = handler(reason)
		}()

		w.mu.Lock()
		w.running = false
		needsRetry := res.Status == "skipped" && res.Reason == "requests-in-flight"
		if needsRetry {
			if reason == "" {
				w.pendingReason = "retry"
			} else {
				w.pendingReason = reason
			}
		}
		hasPending := w.pendingReason != "" || w.scheduled
		w.mu.Unlock()

		if needsRetry {
			w.log.Debug().Str("reason", reason).Msg("Heartbeat wake retrying: requests in flight")
			w.schedule(defaultHeartbeatRetry)
			return
		}
		if hasPending {
			w.schedule(delay)
		}
	})
	w.mu.Unlock()
}

func (w *HeartbeatWake) HasPending() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.pendingReason != "" || w.timer != nil || w.scheduled
}
