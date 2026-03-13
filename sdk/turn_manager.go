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
}

type turnGate struct {
	token chan struct{}
}

// TurnManager provides reusable per-key run helpers.
type TurnManager struct {
	cfg   TurnConfig
	mu    sync.Mutex
	gates map[string]*turnGate
}

// NewTurnManager creates a new helper-managed turn manager.
func NewTurnManager(cfg *TurnConfig) *TurnManager {
	resolved := TurnConfig{
		OneAtATime: true,
	}
	if cfg != nil {
		resolved = *cfg
		if !cfg.OneAtATime {
			resolved.OneAtATime = false
		}
	}
	return &TurnManager{
		cfg:   resolved,
		gates: make(map[string]*turnGate),
	}
}

func (tm *TurnManager) gate(key string) *turnGate {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	g := tm.gates[key]
	if g != nil {
		return g
	}
	g = &turnGate{token: make(chan struct{}, 1)}
	g.token <- struct{}{}
	tm.gates[key] = g
	return g
}

// Acquire reserves the key until the returned release function is called.
func (tm *TurnManager) Acquire(ctx context.Context, key string) (func(), error) {
	if tm == nil || key == "" || !tm.cfg.OneAtATime {
		return func() {}, nil
	}
	g := tm.gate(key)
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-g.token:
		return func() {
			select {
			case g.token <- struct{}{}:
			default:
			}
		}, nil
	}
}

// Run serializes fn for the given key when one-at-a-time is enabled.
func (tm *TurnManager) Run(ctx context.Context, key string, fn func(context.Context) error) error {
	if fn == nil {
		return nil
	}
	release, err := tm.Acquire(ctx, key)
	if err != nil {
		return err
	}
	defer release()
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
