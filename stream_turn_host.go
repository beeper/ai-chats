package agentremote

import "sync"

// Aborter is implemented by any value that can be aborted with a reason string.
// sdk.Turn satisfies this interface.
type Aborter interface {
	Abort(reason string)
}

// StreamTurnHostCallbacks defines the bridge-specific hooks for StreamTurnHost.
type StreamTurnHostCallbacks[S any] struct {
	// GetAborter returns the aborter (typically an *sdk.Turn) from the state, or nil.
	GetAborter func(state *S) Aborter
}

// StreamTurnHost manages a map of stream states keyed by turn ID, providing
// thread-safe drain/abort and state cleanup helpers shared across bridges.
type StreamTurnHost[S any] struct {
	mu        sync.Mutex
	states    map[string]*S
	callbacks StreamTurnHostCallbacks[S]
}

// NewStreamTurnHost creates a new StreamTurnHost.
func NewStreamTurnHost[S any](cb StreamTurnHostCallbacks[S]) *StreamTurnHost[S] {
	return &StreamTurnHost[S]{
		states:    make(map[string]*S),
		callbacks: cb,
	}
}

// Lock acquires the host mutex.
func (h *StreamTurnHost[S]) Lock()   { h.mu.Lock() }

// Unlock releases the host mutex.
func (h *StreamTurnHost[S]) Unlock() { h.mu.Unlock() }

// GetLocked returns the state for turnID. Must be called with the lock held.
func (h *StreamTurnHost[S]) GetLocked(turnID string) *S {
	return h.states[turnID]
}

// SetLocked stores state for turnID. Must be called with the lock held.
func (h *StreamTurnHost[S]) SetLocked(turnID string, state *S) {
	h.states[turnID] = state
}

// DeleteLocked removes a state entry. Must be called with the lock held.
func (h *StreamTurnHost[S]) DeleteLocked(turnID string) {
	delete(h.states, turnID)
}

// DeleteIfMatch removes the entry only if it still points to the given state.
func (h *StreamTurnHost[S]) DeleteIfMatch(turnID string, state *S) {
	h.mu.Lock()
	if h.states[turnID] == state {
		delete(h.states, turnID)
	}
	h.mu.Unlock()
}

// IsActive reports whether a turn ID has an active stream state.
func (h *StreamTurnHost[S]) IsActive(turnID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.states[turnID]
	return ok
}

// DrainAndAbort collects all active turns, clears the map, and aborts each
// turn with the given reason. This is the standard disconnect cleanup path.
func (h *StreamTurnHost[S]) DrainAndAbort(reason string) {
	h.mu.Lock()
	aborters := make([]Aborter, 0, len(h.states))
	for _, state := range h.states {
		if state != nil {
			if a := h.callbacks.GetAborter(state); a != nil {
				aborters = append(aborters, a)
			}
		}
	}
	h.states = make(map[string]*S)
	h.mu.Unlock()
	for _, a := range aborters {
		a.Abort(reason)
	}
}
