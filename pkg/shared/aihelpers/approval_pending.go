package aihelpers

import (
	"context"
	"sort"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Pending approval store
// ---------------------------------------------------------------------------

// Register adds a new pending approval with the given TTL and bridge-specific data.
// Returns the Pending and true if newly created, or the existing one and false
// if a non-expired approval with the same ID already exists.
func (f *ApprovalFlow[D]) Register(approvalID string, ttl time.Duration, data D) (*Pending[D], bool) {
	f.ensureReaperRunning()
	approvalID = strings.TrimSpace(approvalID)
	if approvalID == "" {
		return nil, false
	}
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if existing := f.pending[approvalID]; existing != nil {
		if time.Now().Before(existing.ExpiresAt) {
			return existing, false
		}
		delete(f.pending, approvalID)
	}
	p := &Pending[D]{
		ExpiresAt: time.Now().Add(ttl),
		Data:      data,
		ch:        make(chan ApprovalDecisionPayload, 1),
		done:      make(chan struct{}),
	}
	f.pending[approvalID] = p
	f.wakeReaper()
	return p, true
}

// Get returns the pending approval for the given id, or nil if not found.
func (f *ApprovalFlow[D]) Get(approvalID string) *Pending[D] {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pending[approvalID]
}

// SetData updates the Data field on a pending approval under the lock.
// Returns false if the approval is not found.
func (f *ApprovalFlow[D]) SetData(approvalID string, updater func(D) D) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.pending[approvalID]
	if p == nil {
		return false
	}
	p.Data = updater(p.Data)
	return true
}

// Drop removes a pending approval and its associated prompt from both stores.
func (f *ApprovalFlow[D]) Drop(approvalID string) {
	if f == nil {
		return
	}
	f.finalizeWithPromptVersion(approvalID, nil, false, 0)
}

// normalizeDecisionID trims the approvalID and ensures decision.ApprovalID is set.
// Returns the trimmed approvalID and false if it is empty.
func normalizeDecisionID(approvalID string, decision *ApprovalDecisionPayload) (string, bool) {
	approvalID = strings.TrimSpace(approvalID)
	if approvalID == "" {
		return "", false
	}
	if strings.TrimSpace(decision.ApprovalID) == "" {
		decision.ApprovalID = approvalID
	}
	return approvalID, true
}

// FinishResolved finalizes a terminal approval by editing the approval prompt to
// its final state and cleaning up bridge-authored placeholder reactions.
func (f *ApprovalFlow[D]) FinishResolved(approvalID string, decision ApprovalDecisionPayload) {
	if f == nil {
		return
	}
	approvalID, ok := normalizeDecisionID(approvalID, &decision)
	if !ok {
		return
	}
	f.finalizeWithPromptVersion(approvalID, &decision, true, 0)
}

// ResolveExternal finalizes a remote allow/deny decision. The bridge declares
// whether the decision originated from the user or the agent/system and the
// shared approval flow manages the terminal Matrix reactions accordingly.
func (f *ApprovalFlow[D]) ResolveExternal(ctx context.Context, approvalID string, decision ApprovalDecisionPayload) {
	if f == nil {
		return
	}
	approvalID, ok := normalizeDecisionID(approvalID, &decision)
	if !ok {
		return
	}
	if normalizeApprovalResolutionOrigin(decision.ResolvedBy) == "" {
		decision.ResolvedBy = ApprovalResolutionOriginAgent
	}
	prompt, hasPrompt := f.promptRegistration(approvalID)
	if err := f.Resolve(approvalID, decision); err != nil {
		return
	}
	if hasPrompt && decision.ResolvedBy == ApprovalResolutionOriginUser {
		f.mirrorRemoteDecisionReaction(ctx, prompt, decision)
	}
	f.FinishResolved(approvalID, decision)
}

// FindByData iterates pending approvals and returns the id of the first one
// for which the predicate returns true. Returns "" if none match.
func (f *ApprovalFlow[D]) FindByData(predicate func(data D) bool) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, p := range f.pending {
		if p != nil && predicate(p.Data) {
			return id
		}
	}
	return ""
}

func (f *ApprovalFlow[D]) PendingIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	ids := make([]string, 0, len(f.pending))
	for id := range f.pending {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Resolve programmatically delivers a decision to a pending approval's channel.
// Use this when a decision arrives from an external source (e.g. the upstream
// server or auto-approval) rather than a Matrix reaction.
// Unlike HandleReaction, Resolve does NOT drop the pending entry — the caller
// (typically Wait or an explicit Drop) is responsible for cleanup.
func (f *ApprovalFlow[D]) Resolve(approvalID string, decision ApprovalDecisionPayload) error {
	approvalID = strings.TrimSpace(approvalID)
	if approvalID == "" {
		return ErrApprovalMissingID
	}
	f.mu.Lock()
	p := f.pending[approvalID]
	f.mu.Unlock()
	if p == nil {
		return ErrApprovalUnknown
	}
	if time.Now().After(p.ExpiresAt) {
		f.finishTimedOutApproval(approvalID)
		return ErrApprovalExpired
	}
	select {
	case p.ch <- decision:
		f.cancelPendingTimeout(approvalID)
		return nil
	default:
		return ErrApprovalAlreadyHandled
	}
}

// Wait blocks until a decision arrives via reaction, the approval expires,
// or ctx is cancelled. Only useful for channel-based flows (DeliverDecision is nil).
func (f *ApprovalFlow[D]) Wait(ctx context.Context, approvalID string) (ApprovalDecisionPayload, bool) {
	var zero ApprovalDecisionPayload
	approvalID = strings.TrimSpace(approvalID)
	if approvalID == "" {
		return zero, false
	}
	f.mu.Lock()
	p := f.pending[approvalID]
	f.mu.Unlock()
	if p == nil {
		return zero, false
	}
	select {
	case d := <-p.ch:
		return d, true
	default:
	}
	timeout := time.Until(p.ExpiresAt)
	if timeout <= 0 {
		f.finishTimedOutApproval(approvalID)
		return zero, false
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case d := <-p.ch:
		return d, true
	case <-timer.C:
		f.finishTimedOutApproval(approvalID)
		return zero, false
	case <-ctx.Done():
		return zero, false
	}
}
