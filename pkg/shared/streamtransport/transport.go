package streamtransport

import (
	"strings"
	"sync"
	"time"
)

type Mode string

const (
	ModeEphemeral     Mode = "ephemeral"
	ModeDebouncedEdit Mode = "debounced_edit"
)

const (
	DefaultMode           = ModeEphemeral
	DefaultEditDebounceMs = 250
	staleTurnTTL          = 30 * time.Minute
)

// ResolveMode returns ModeDebouncedEdit if configValue matches, otherwise DefaultMode.
func ResolveMode(configValue string) Mode {
	if strings.TrimSpace(configValue) == string(ModeDebouncedEdit) {
		return ModeDebouncedEdit
	}
	return ModeEphemeral
}

// ResolveDebounceDuration returns configMs as a duration, falling back to DefaultEditDebounceMs.
func ResolveDebounceDuration(configMs int) time.Duration {
	if configMs <= 0 {
		configMs = DefaultEditDebounceMs
	}
	return time.Duration(configMs) * time.Millisecond
}

type turnGateState struct {
	lastBody string
	lastAt   time.Time
}

// EditDebounceGate tracks per-turn edit emission to avoid duplicate and overly-frequent edits.
type EditDebounceGate struct {
	mu            sync.Mutex
	turns         map[string]*turnGateState
	lastEvictedAt time.Time
}

func NewEditDebounceGate() *EditDebounceGate {
	return &EditDebounceGate{
		turns: make(map[string]*turnGateState),
	}
}

func (g *EditDebounceGate) ShouldEmit(turnID, body string, now time.Time, debounce time.Duration) bool {
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return false
	}
	if now.IsZero() {
		now = time.Now()
	}
	if debounce < 0 {
		debounce = 0
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if now.Sub(g.lastEvictedAt) >= staleTurnTTL {
		for id, s := range g.turns {
			if !s.lastAt.IsZero() && now.Sub(s.lastAt) >= staleTurnTTL {
				delete(g.turns, id)
			}
		}
		g.lastEvictedAt = now
	}

	state, ok := g.turns[turnID]
	if !ok {
		state = &turnGateState{}
		g.turns[turnID] = state
	}
	if body == state.lastBody {
		return false
	}
	if !state.lastAt.IsZero() && now.Sub(state.lastAt) < debounce {
		return false
	}
	state.lastBody = body
	state.lastAt = now
	return true
}

func (g *EditDebounceGate) Clear(turnID string) {
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return
	}
	g.mu.Lock()
	delete(g.turns, turnID)
	g.mu.Unlock()
}
