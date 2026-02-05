package connector

import (
	"context"
	"sync"
	"time"

	"maunium.net/go/mautrix/bridgev2"
)

const (
	// typingRefreshInterval is how often to resend typing indicator (Matrix typing expires after ~30s)
	typingRefreshInterval = 6 * time.Second
	// typingTTL is the maximum time to keep typing active before auto-stopping
	typingTTL = 2 * time.Minute
)

// TypingController manages typing indicators with TTL and refresh.
// Similar to OpenClaw's TypingController pattern.
type TypingController struct {
	client *AIClient
	portal *bridgev2.Portal
	ctx    context.Context
	interval time.Duration
	ttl      time.Duration

	mu          sync.Mutex
	active      bool
	sealed      bool // Once sealed, typing cannot be restarted
	runComplete bool
	dispatchIdle bool
	ticker      *time.Ticker
	ttlTimer    *time.Timer
	stopChan    chan struct{}
}

type TypingControllerOptions struct {
	Interval time.Duration
	TTL      time.Duration
}

// NewTypingController creates a new typing controller.
func NewTypingController(client *AIClient, ctx context.Context, portal *bridgev2.Portal, opts TypingControllerOptions) *TypingController {
	interval := opts.Interval
	if interval == 0 {
		interval = typingRefreshInterval
	}
	ttl := opts.TTL
	if ttl == 0 {
		ttl = typingTTL
	}
	return &TypingController{
		client:   client,
		portal:   portal,
		ctx:      ctx,
		interval: interval,
		ttl:      ttl,
		stopChan: make(chan struct{}),
	}
}

// Start begins the typing indicator with automatic refresh.
func (tc *TypingController) Start() {
	tc.mu.Lock()
	defer tc.mu.Unlock()

	if tc.sealed || tc.active {
		return
	}
	if tc.interval <= 0 {
		return
	}

	tc.active = true

	// Send initial typing indicator
	tc.client.setModelTyping(tc.ctx, tc.portal, true)

	// Start refresh ticker
	tc.ticker = time.NewTicker(tc.interval)
	tickerChan := tc.ticker.C // Capture before goroutine starts to avoid race

	// Start TTL timer
	if tc.ttl > 0 {
		tc.ttlTimer = time.AfterFunc(tc.ttl, func() {
			tc.client.log.Debug().Msg("Typing TTL reached, stopping typing indicator")
			tc.Stop()
		})
	}

	// Start refresh loop with captured channel
	go tc.refreshLoop(tickerChan)
}

// refreshLoop sends typing indicators at regular intervals.
// tickerChan is passed as a parameter to avoid race condition with Stop() setting tc.ticker to nil.
func (tc *TypingController) refreshLoop(tickerChan <-chan time.Time) {
	for {
		select {
		case <-tc.stopChan:
			return
		case <-tickerChan:
			tc.mu.Lock()
			if tc.sealed || !tc.active {
				tc.mu.Unlock()
				return
			}
			tc.mu.Unlock()
			tc.client.setModelTyping(tc.ctx, tc.portal, true)
		}
	}
}

// RefreshTTL resets the TTL timer, keeping typing active longer.
// Call this when activity occurs (tool calls, text chunks).
func (tc *TypingController) RefreshTTL() {
	tc.mu.Lock()
	defer tc.mu.Unlock()

	if tc.sealed || !tc.active || tc.ttlTimer == nil {
		return
	}

	tc.ttlTimer.Reset(tc.ttl)
}

// MarkRunComplete marks the AI run as complete.
// Typing will stop when both run is complete and dispatch is idle.
func (tc *TypingController) MarkRunComplete() {
	tc.mu.Lock()
	tc.runComplete = true
	tc.mu.Unlock()

	// Check if we should stop
	tc.maybeStop()
}

// MarkDispatchIdle marks the dispatcher as idle.
func (tc *TypingController) MarkDispatchIdle() {
	tc.mu.Lock()
	tc.dispatchIdle = true
	tc.mu.Unlock()

	// Check if we should stop
	tc.maybeStop()
}

// maybeStop stops typing if conditions are met.
func (tc *TypingController) maybeStop() {
	tc.mu.Lock()
	if !tc.active || tc.sealed {
		tc.mu.Unlock()
		return
	}
	if tc.runComplete && tc.dispatchIdle {
		tc.mu.Unlock()
		tc.Stop()
	} else {
		tc.mu.Unlock()
	}
}

// Stop stops the typing indicator and cleans up.
func (tc *TypingController) Stop() {
	tc.mu.Lock()
	if tc.sealed {
		tc.mu.Unlock()
		return
	}

	tc.sealed = true
	tc.active = false

	// Stop ticker
	if tc.ticker != nil {
		tc.ticker.Stop()
		tc.ticker = nil
	}

	// Stop TTL timer
	if tc.ttlTimer != nil {
		tc.ttlTimer.Stop()
		tc.ttlTimer = nil
	}

	// Signal refresh loop to stop
	close(tc.stopChan)

	tc.mu.Unlock()

	// Send stop typing
	tc.client.setModelTyping(tc.ctx, tc.portal, false)
}

// IsActive returns whether typing is currently active.
func (tc *TypingController) IsActive() bool {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	return tc.active && !tc.sealed
}
