package sdk

import (
	"context"
	"sync"
	"time"
)

// TurnConfig configures helper-managed turn serialization and coalescing.
type TurnConfig struct {
	OneAtATime bool
	DebounceMs int
	QueueSize  int
	// IdleTimeoutMs aborts turns that stop emitting streamed parts for too long.
	// Zero uses the SDK default; a negative value disables the watchdog.
	IdleTimeoutMs int

	// KeyFunc customizes the serialization key. By default, the portal ID is
	// used directly. Multi-agent rooms can return "roomID:agentID" so that
	// agents within the same room run concurrently.
	KeyFunc func(portalID string) string
}

type turnGate struct {
	token   chan struct{}
	waiters int // number of goroutines waiting to acquire
}

// TurnManager provides reusable per-key run helpers.
type TurnManager struct {
	cfg   TurnConfig
	mu    sync.Mutex
	gates map[string]*turnGate
}

// NewTurnManager creates a new helper-managed turn manager.
func NewTurnManager(cfg *TurnConfig) *TurnManager {
	resolved := TurnConfig{OneAtATime: true}
	if cfg != nil {
		resolved = *cfg
	}
	return &TurnManager{
		cfg:   resolved,
		gates: make(map[string]*turnGate),
	}
}

// ResolveKey applies the configured KeyFunc (or identity) to a portal ID.
func (tm *TurnManager) ResolveKey(portalID string) string {
	if tm != nil && tm.cfg.KeyFunc != nil {
		return tm.cfg.KeyFunc(portalID)
	}
	return portalID
}

func (tm *TurnManager) gate(key string) *turnGate {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	if g, ok := tm.gates[key]; ok {
		return g
	}
	g := &turnGate{token: make(chan struct{}, 1)}
	g.token <- struct{}{}
	tm.gates[key] = g
	return g
}

// evictGate removes the gate entry if no one is waiting and the token is available.
func (tm *TurnManager) evictGate(key string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	g, ok := tm.gates[key]
	if !ok {
		return
	}
	if g.waiters > 0 {
		return
	}
	// Only evict if the token is available (no active run).
	select {
	case <-g.token:
		delete(tm.gates, key)
	default:
	}
}

// Acquire reserves the key until the returned release function is called.
func (tm *TurnManager) Acquire(ctx context.Context, key string) (func(), error) {
	if tm == nil || key == "" || !tm.cfg.OneAtATime {
		return func() {}, nil
	}
	g := tm.gate(key)

	tm.mu.Lock()
	g.waiters++
	tm.mu.Unlock()

	select {
	case <-ctx.Done():
		tm.mu.Lock()
		g.waiters--
		tm.mu.Unlock()
		tm.evictGate(key)
		return nil, ctx.Err()
	case <-g.token:
		tm.mu.Lock()
		g.waiters--
		tm.mu.Unlock()
		return func() {
			select {
			case g.token <- struct{}{}:
			default:
			}
			tm.evictGate(key)
		}, nil
	}
}

// Run serializes fn for the given key when one-at-a-time is enabled.
// When DebounceMs > 0, the first call is delayed to coalesce rapid messages.
func (tm *TurnManager) Run(ctx context.Context, key string, fn func(context.Context) error) error {
	if fn == nil {
		return nil
	}
	release, err := tm.Acquire(ctx, key)
	if err != nil {
		return err
	}
	defer release()

	// Debounce: delay execution to coalesce rapid messages.
	if d := tm.DebounceWindow(); d > 0 {
		timer := time.NewTimer(d)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}

	return fn(ctx)
}

// IsActive reports whether the key currently has an active run.
func (tm *TurnManager) IsActive(key string) bool {
	if tm == nil || key == "" || !tm.cfg.OneAtATime {
		return false
	}
	g := tm.gate(key)
	select {
	case token := <-g.token:
		g.token <- token
		return false
	default:
		return true
	}
}

// DebounceWindow returns the configured debounce interval.
func (tm *TurnManager) DebounceWindow() time.Duration {
	if tm == nil || tm.cfg.DebounceMs <= 0 {
		return 0
	}
	return time.Duration(tm.cfg.DebounceMs) * time.Millisecond
}

// QueueLimit returns the configured queue size hint.
func (tm *TurnManager) QueueLimit() int {
	if tm == nil {
		return 0
	}
	return tm.cfg.QueueSize
}
