package bridgeadapter

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

// Shared sentinel errors for approval resolution.
var (
	ErrApprovalMissingID      = errors.New("missing approval id")
	ErrApprovalMissingRoom    = errors.New("missing room id")
	ErrApprovalOnlyOwner      = errors.New("only the owner can approve")
	ErrApprovalUnknown        = errors.New("unknown or expired approval id")
	ErrApprovalWrongRoom      = errors.New("approval id does not belong to this room")
	ErrApprovalExpired        = errors.New("approval expired")
	ErrApprovalAlreadyHandled = errors.New("approval already resolved")
)

// ApprovalErrorToastText maps an approval error to a user-facing toast string.
func ApprovalErrorToastText(err error) string {
	if err == nil {
		return ""
	}
	switch {
	case errors.Is(err, ErrApprovalOnlyOwner):
		return "Only the owner can approve."
	case errors.Is(err, ErrApprovalWrongRoom):
		return "That approval request belongs to a different room."
	case errors.Is(err, ErrApprovalExpired), errors.Is(err, ErrApprovalUnknown):
		return "That approval request is expired or no longer valid."
	case errors.Is(err, ErrApprovalAlreadyHandled):
		return "That approval request was already handled."
	case errors.Is(err, ErrApprovalMissingID):
		return "Missing approval ID."
	default:
		return strings.TrimSpace(err.Error())
	}
}

// ApprovalManager[D] manages pending approvals with channel-based resolution.
// D is the decision type sent through the channel (bridge-specific).
type ApprovalManager[D any] struct {
	mu      sync.Mutex
	pending map[string]*PendingApproval[D]
}

// PendingApproval represents a single pending approval waiting for a decision.
type PendingApproval[D any] struct {
	ExpiresAt time.Time
	Data      any // Bridge-specific metadata (type-assert at call site)
	ch        chan D
}

// NewApprovalManager creates a new ApprovalManager.
func NewApprovalManager[D any]() *ApprovalManager[D] {
	return &ApprovalManager[D]{
		pending: make(map[string]*PendingApproval[D]),
	}
}

// Register adds a new pending approval with the given TTL.
// Returns the PendingApproval and true if newly created, or the existing one and false if already registered.
func (m *ApprovalManager[D]) Register(id string, ttl time.Duration, data any) (*PendingApproval[D], bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, false
	}
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing := m.pending[id]; existing != nil {
		return existing, false
	}
	p := &PendingApproval[D]{
		ExpiresAt: time.Now().Add(ttl),
		Data:      data,
		ch:        make(chan D, 1),
	}
	m.pending[id] = p
	return p, true
}

// Resolve delivers a decision to the pending approval identified by id.
// Returns ErrApprovalUnknown if not found, ErrApprovalExpired if expired,
// or ErrApprovalAlreadyHandled if the channel is already full.
func (m *ApprovalManager[D]) Resolve(id string, decision D) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return ErrApprovalMissingID
	}
	m.mu.Lock()
	p := m.pending[id]
	m.mu.Unlock()
	if p == nil {
		return ErrApprovalUnknown
	}
	if time.Now().After(p.ExpiresAt) {
		m.Drop(id)
		return ErrApprovalExpired
	}
	select {
	case p.ch <- decision:
		return nil
	default:
		m.Drop(id)
		return ErrApprovalAlreadyHandled
	}
}

// Wait blocks until a decision arrives, the approval expires, or ctx is cancelled.
// Returns the decision and true on success, or zero-value and false on timeout/cancel.
func (m *ApprovalManager[D]) Wait(ctx context.Context, id string) (D, bool) {
	var zero D
	id = strings.TrimSpace(id)
	if id == "" {
		return zero, false
	}
	m.mu.Lock()
	p := m.pending[id]
	m.mu.Unlock()
	if p == nil {
		return zero, false
	}
	timeout := time.Until(p.ExpiresAt)
	if timeout <= 0 {
		m.Drop(id)
		return zero, false
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case d := <-p.ch:
		m.Drop(id)
		return d, true
	case <-timer.C:
		m.Drop(id)
		return zero, false
	case <-ctx.Done():
		m.Drop(id)
		return zero, false
	}
}

// Drop removes a pending approval from the manager.
func (m *ApprovalManager[D]) Drop(id string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	m.mu.Lock()
	delete(m.pending, id)
	m.mu.Unlock()
}

// Get returns the pending approval for the given id, or nil if not found.
func (m *ApprovalManager[D]) Get(id string) *PendingApproval[D] {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pending[id]
}

// FindByData iterates pending approvals and returns the id of the first one
// for which the predicate returns true. Returns "" if none match.
func (m *ApprovalManager[D]) FindByData(predicate func(data any) bool) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, p := range m.pending {
		if p != nil && predicate(p.Data) {
			return id
		}
	}
	return ""
}

// SetData updates the Data field on a pending approval under the lock.
// Returns false if the approval is not found.
func (m *ApprovalManager[D]) SetData(id string, updater func(data any) any) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.pending[id]
	if p == nil {
		return false
	}
	p.Data = updater(p.Data)
	return true
}
