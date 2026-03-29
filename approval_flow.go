package agentremote

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"maunium.net/go/mautrix/bridgev2"

	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// ApprovalReactionHandler is the interface used by BaseReactionHandler to
// dispatch reactions to the approval system without knowing the concrete type.
type ApprovalReactionHandler interface {
	HandleReaction(ctx context.Context, msg *bridgev2.MatrixReaction) bool
}

// ApprovalReactionRemoveHandler is an optional extension for handling reaction removals.
type ApprovalReactionRemoveHandler interface {
	HandleReactionRemove(ctx context.Context, msg *bridgev2.MatrixReactionRemove) bool
}

const approvalWrongTargetMSSMessage = "React to the approval notice message to respond."
const approvalResolvedMSSMessage = "That approval request was already handled and can't be changed."

// ApprovalFlowConfig holds the bridge-specific callbacks for ApprovalFlow.
type ApprovalFlowConfig[D any] struct {
	// Login returns the current UserLogin. Required.
	Login func() *bridgev2.UserLogin

	// Sender returns the EventSender to use for a given portal (e.g. the agent ghost).
	Sender func(portal *bridgev2.Portal) bridgev2.EventSender

	// BackgroundContext optionally returns a context detached from the request lifecycle.
	BackgroundContext func(ctx context.Context) context.Context

	// RoomIDFromData extracts the stored room ID from pending data for validation.
	// Return "" to skip the room check.
	RoomIDFromData func(data D) id.RoomID

	// DeliverDecision is called for non-channel flows when a valid reaction resolves
	// an approval. The flow has already validated owner, expiration, and room.
	// If nil, the flow is channel-based: decisions are delivered via an internal
	// channel and retrieved with Wait().
	DeliverDecision func(ctx context.Context, portal *bridgev2.Portal, pending *Pending[D], decision ApprovalDecisionPayload) error

	// SendNotice sends a system notice to a portal. Used for error toasts.
	SendNotice func(ctx context.Context, portal *bridgev2.Portal, msg string)

	// DBMetadata produces bridge-specific metadata for the approval prompt message.
	// If nil, a default *BaseMessageMetadata is used.
	DBMetadata func(prompt ApprovalPromptMessage) any

	IDPrefix    string
	LogKey      string
	SendTimeout time.Duration
}

// Pending represents a single pending approval.
type Pending[D any] struct {
	ExpiresAt time.Time
	Data      D
	ch        chan ApprovalDecisionPayload
	done      chan struct{} // closed when the approval is finalized
}

type resolvedApprovalPrompt struct {
	Prompt    ApprovalPromptRegistration
	Decision  ApprovalDecisionPayload
	ExpiresAt time.Time
}

// closeDone marks the pending approval as finalized. Safe to call multiple times.
func (p *Pending[D]) closeDone() {
	select {
	case <-p.done:
	default:
		close(p.done)
	}
}

// ApprovalFlow owns the full lifecycle of approval prompts and pending approvals.
// D is the bridge-specific pending data type.
type ApprovalFlow[D any] struct {
	mu      sync.Mutex
	pending map[string]*Pending[D]

	// Prompt store (inlined from ApprovalPromptStore).
	promptsByApproval       map[string]*ApprovalPromptRegistration
	promptsByMsgID          map[networkid.MessageID]string
	reactionTargetsByMsgID  map[networkid.MessageID]string
	resolvedByMsgID         map[networkid.MessageID]*resolvedApprovalPrompt
	resolvedByReactionMsgID map[networkid.MessageID]*resolvedApprovalPrompt

	login           func() *bridgev2.UserLogin
	sender          func(portal *bridgev2.Portal) bridgev2.EventSender
	backgroundCtx   func(ctx context.Context) context.Context
	roomIDFromData  func(data D) id.RoomID
	deliverDecision func(ctx context.Context, portal *bridgev2.Portal, pending *Pending[D], decision ApprovalDecisionPayload) error
	sendNotice      func(ctx context.Context, portal *bridgev2.Portal, msg string)
	dbMetadata      func(prompt ApprovalPromptMessage) any
	idPrefix        string
	logKey          string
	sendTimeout     time.Duration

	// Reaper goroutine fields.
	reaperStop   chan struct{}
	reaperNotify chan struct{}

	// Test hooks (nil in production).
	testResolvePortal                 func(ctx context.Context, login *bridgev2.UserLogin, roomID id.RoomID) (*bridgev2.Portal, error)
	testEditPromptToResolvedState     func(ctx context.Context, login *bridgev2.UserLogin, portal *bridgev2.Portal, sender bridgev2.EventSender, prompt ApprovalPromptRegistration, decision ApprovalDecisionPayload)
	testRedactPromptPlaceholderReacts func(ctx context.Context, login *bridgev2.UserLogin, portal *bridgev2.Portal, sender bridgev2.EventSender, prompt ApprovalPromptRegistration, opts ApprovalPromptReactionCleanupOptions) error
	testMirrorRemoteDecisionReaction  func(ctx context.Context, login *bridgev2.UserLogin, portal *bridgev2.Portal, sender bridgev2.EventSender, prompt ApprovalPromptRegistration, reactionKey string)
	testRedactSingleReaction          func(msg *bridgev2.MatrixReaction)
	testSendMessageStatus             func(ctx context.Context, portal *bridgev2.Portal, evt *event.Event, status bridgev2.MessageStatus)
}

// NewApprovalFlow creates an ApprovalFlow from the given config.
// Call Close() when the flow is no longer needed to stop the reaper goroutine.
func NewApprovalFlow[D any](cfg ApprovalFlowConfig[D]) *ApprovalFlow[D] {
	timeout := cfg.SendTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	f := &ApprovalFlow[D]{
		pending:                 make(map[string]*Pending[D]),
		promptsByApproval:       make(map[string]*ApprovalPromptRegistration),
		promptsByMsgID:          make(map[networkid.MessageID]string),
		reactionTargetsByMsgID:  make(map[networkid.MessageID]string),
		resolvedByMsgID:         make(map[networkid.MessageID]*resolvedApprovalPrompt),
		resolvedByReactionMsgID: make(map[networkid.MessageID]*resolvedApprovalPrompt),
		login:                   cfg.Login,
		sender:                  cfg.Sender,
		backgroundCtx:           cfg.BackgroundContext,
		roomIDFromData:          cfg.RoomIDFromData,
		deliverDecision:         cfg.DeliverDecision,
		sendNotice:              cfg.SendNotice,
		dbMetadata:              cfg.DBMetadata,
		idPrefix:                cfg.IDPrefix,
		logKey:                  cfg.LogKey,
		sendTimeout:             timeout,
		reaperStop:              make(chan struct{}),
		reaperNotify:            make(chan struct{}, 1),
	}
	go f.runReaper()
	return f
}

// Close stops the reaper goroutine. Safe to call multiple times.
func (f *ApprovalFlow[D]) Close() {
	if f == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closeReaperLocked()
}

func (f *ApprovalFlow[D]) closeReaperLocked() {
	select {
	case <-f.reaperStop:
	default:
		close(f.reaperStop)
	}
}

func (f *ApprovalFlow[D]) ensureReaperRunning() {
	if f == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	select {
	case <-f.reaperStop:
		f.reaperStop = make(chan struct{})
		f.reaperNotify = make(chan struct{}, 1)
		go f.runReaper()
	default:
	}
}

func (f *ApprovalFlow[D]) wakeReaper() {
	if f == nil {
		return
	}
	select {
	case f.reaperNotify <- struct{}{}:
	default:
	}
}

const reaperMaxInterval = 30 * time.Second

func (f *ApprovalFlow[D]) runReaper() {
	timer := time.NewTimer(reaperMaxInterval)
	defer timer.Stop()
	for {
		select {
		case <-f.reaperStop:
			return
		case <-timer.C:
			f.reapExpired()
			timer.Reset(f.nextReaperDelay())
		case <-f.reaperNotify:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(f.nextReaperDelay())
		}
	}
}

// earliestExpiry returns the earlier of a and b, ignoring zero values.
func earliestExpiry(a, b time.Time) time.Time {
	if a.IsZero() {
		return b
	}
	if b.IsZero() || a.Before(b) {
		return a
	}
	return b
}

func approvalPendingResolved[D any](p *Pending[D]) bool {
	if p == nil {
		return false
	}
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

// nextReaperDelay returns the duration until the earliest pending/prompt expiry,
// capped at reaperMaxInterval.
func (f *ApprovalFlow[D]) nextReaperDelay() time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	earliest := time.Time{}
	for _, p := range f.pending {
		if approvalPendingResolved(p) {
			continue
		}
		earliest = earliestExpiry(earliest, p.ExpiresAt)
	}
	for approvalID, entry := range f.promptsByApproval {
		if approvalPendingResolved(f.pending[approvalID]) {
			continue
		}
		earliest = earliestExpiry(earliest, entry.ExpiresAt)
	}
	if earliest.IsZero() {
		return reaperMaxInterval
	}
	delay := time.Until(earliest)
	if delay <= 0 {
		return time.Millisecond
	}
	if delay > reaperMaxInterval {
		return reaperMaxInterval
	}
	return delay
}

func (f *ApprovalFlow[D]) reapExpired() {
	now := time.Now()
	candidates := make(map[string]expiredApprovalCandidate[D])
	f.mu.Lock()
	// Finalize pending approvals whose own TTL has elapsed.
	for aid, p := range f.pending {
		if approvalPendingResolved(p) {
			continue
		}
		if !p.ExpiresAt.IsZero() && now.After(p.ExpiresAt) {
			candidate := candidates[aid]
			candidate.approvalID = aid
			candidate.pending = p
			candidate.expiredByPending = true
			candidates[aid] = candidate
		}
	}
	// Also finalize pending approvals whose associated prompt has expired.
	for aid, entry := range f.promptsByApproval {
		pending := f.pending[aid]
		if approvalPendingResolved(pending) {
			continue
		}
		if !entry.ExpiresAt.IsZero() && now.After(entry.ExpiresAt) {
			if pending != nil {
				candidate := candidates[aid]
				candidate.approvalID = aid
				candidate.pending = pending
				candidate.prompt = entry
				candidate.expiredByPrompt = true
				candidates[aid] = candidate
			} else {
				// Orphan prompt — clean it up.
				if entry.PromptMessageID != "" {
					delete(f.promptsByMsgID, entry.PromptMessageID)
				}
				if entry.ReactionTargetMessageID != "" {
					delete(f.reactionTargetsByMsgID, entry.ReactionTargetMessageID)
				}
				delete(f.promptsByApproval, aid)
			}
		}
	}
	f.mu.Unlock()
	for _, candidate := range candidates {
		f.finalizeExpiredCandidate(now, candidate)
	}
}

type expiredApprovalCandidate[D any] struct {
	approvalID       string
	pending          *Pending[D]
	prompt           *ApprovalPromptRegistration
	expiredByPending bool
	expiredByPrompt  bool
}

func (f *ApprovalFlow[D]) finalizeExpiredCandidate(now time.Time, candidate expiredApprovalCandidate[D]) {
	if candidate.approvalID == "" || candidate.pending == nil {
		return
	}
	var promptVersion uint64
	expiredByPending := false
	expiredByPrompt := false

	f.mu.Lock()
	currentPending := f.pending[candidate.approvalID]
	if currentPending == candidate.pending && !approvalPendingResolved(currentPending) {
		if candidate.expiredByPending && !currentPending.ExpiresAt.IsZero() && now.After(currentPending.ExpiresAt) {
			expiredByPending = true
		}
		if candidate.expiredByPrompt {
			currentPrompt := f.promptsByApproval[candidate.approvalID]
			if currentPrompt == candidate.prompt && currentPrompt != nil && !currentPrompt.ExpiresAt.IsZero() && now.After(currentPrompt.ExpiresAt) {
				expiredByPrompt = true
				promptVersion = currentPrompt.PromptVersion
			}
		}
	}
	f.mu.Unlock()

	switch {
	case expiredByPending:
		f.finishTimedOutApproval(candidate.approvalID)
	case expiredByPrompt:
		f.finishTimedOutApprovalWithPromptVersion(candidate.approvalID, promptVersion)
	}
}

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

// ---------------------------------------------------------------------------
// Prompt store (inlined)
// ---------------------------------------------------------------------------

// registerPrompt adds or replaces a prompt registration.
// Must be called with f.mu held.
func (f *ApprovalFlow[D]) registerPromptLocked(reg ApprovalPromptRegistration) {
	reg.ApprovalID = strings.TrimSpace(reg.ApprovalID)
	if reg.ApprovalID == "" {
		return
	}
	reg.ToolCallID = strings.TrimSpace(reg.ToolCallID)
	reg.ToolName = strings.TrimSpace(reg.ToolName)
	reg.TurnID = strings.TrimSpace(reg.TurnID)

	prev := f.promptsByApproval[reg.ApprovalID]
	if reg.PromptVersion == 0 && prev != nil {
		reg.PromptVersion = prev.PromptVersion
	}
	if prev != nil && prev.PromptMessageID != "" {
		delete(f.promptsByMsgID, prev.PromptMessageID)
	}
	if prev != nil && prev.ReactionTargetMessageID != "" {
		delete(f.reactionTargetsByMsgID, prev.ReactionTargetMessageID)
	}
	copyReg := reg
	f.promptsByApproval[reg.ApprovalID] = &copyReg
	if reg.PromptMessageID != "" {
		f.promptsByMsgID[reg.PromptMessageID] = reg.ApprovalID
	}
	if reg.ReactionTargetMessageID != "" {
		f.reactionTargetsByMsgID[reg.ReactionTargetMessageID] = reg.ApprovalID
	}
}

// bindPromptTargetLocked associates a prompt with its remote message ID. It
// returns the prompt generation that should own any timeout goroutine.
// Must be called with f.mu held.
func (f *ApprovalFlow[D]) bindPromptTargetLocked(approvalID string, messageID networkid.MessageID) (uint64, bool) {
	approvalID = strings.TrimSpace(approvalID)
	messageID = networkid.MessageID(strings.TrimSpace(string(messageID)))
	if approvalID == "" || messageID == "" {
		return 0, false
	}
	entry := f.promptsByApproval[approvalID]
	if entry == nil {
		return 0, false
	}
	if entry.PromptMessageID != "" {
		delete(f.promptsByMsgID, entry.PromptMessageID)
	}
	if entry.ReactionTargetMessageID != "" {
		f.reactionTargetsByMsgID[entry.ReactionTargetMessageID] = approvalID
	}
	entry.PromptVersion++
	entry.PromptMessageID = messageID
	f.promptsByMsgID[messageID] = approvalID
	return entry.PromptVersion, true
}

func (f *ApprovalFlow[D]) promptRegistration(approvalID string) (ApprovalPromptRegistration, bool) {
	approvalID = strings.TrimSpace(approvalID)
	if approvalID == "" {
		return ApprovalPromptRegistration{}, false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	entry := f.promptsByApproval[approvalID]
	if entry == nil {
		return ApprovalPromptRegistration{}, false
	}
	return *entry, true
}

func (f *ApprovalFlow[D]) resolvedPromptByTarget(targetMessageID networkid.MessageID) (resolvedApprovalPrompt, bool) {
	if f == nil {
		return resolvedApprovalPrompt{}, false
	}
	targetMessageID = networkid.MessageID(strings.TrimSpace(string(targetMessageID)))
	if targetMessageID == "" {
		return resolvedApprovalPrompt{}, false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pruneExpiredResolvedPromptsLocked(time.Now())
	if entry := f.resolvedByMsgID[targetMessageID]; entry != nil {
		return *entry, true
	}
	if entry := f.resolvedByReactionMsgID[targetMessageID]; entry != nil {
		return *entry, true
	}
	return resolvedApprovalPrompt{}, false
}

func (f *ApprovalFlow[D]) pruneExpiredResolvedPromptsLocked(now time.Time) {
	if now.IsZero() {
		now = time.Now()
	}
	for messageID, entry := range f.resolvedByMsgID {
		if entry == nil || entry.ExpiresAt.IsZero() || now.Before(entry.ExpiresAt) {
			continue
		}
		delete(f.resolvedByMsgID, messageID)
	}
	for messageID, entry := range f.resolvedByReactionMsgID {
		if entry == nil || entry.ExpiresAt.IsZero() || now.Before(entry.ExpiresAt) {
			continue
		}
		delete(f.resolvedByReactionMsgID, messageID)
	}
}

func (f *ApprovalFlow[D]) rememberResolvedPromptLocked(prompt ApprovalPromptRegistration, decision ApprovalDecisionPayload) {
	f.pruneExpiredResolvedPromptsLocked(time.Now())
	if prompt.PromptMessageID == "" && prompt.ReactionTargetMessageID == "" {
		return
	}
	resolved := &resolvedApprovalPrompt{
		Prompt:    prompt,
		Decision:  decision,
		ExpiresAt: prompt.ExpiresAt,
	}
	if prompt.PromptMessageID != "" {
		f.resolvedByMsgID[prompt.PromptMessageID] = resolved
	}
	if prompt.ReactionTargetMessageID != "" {
		f.resolvedByReactionMsgID[prompt.ReactionTargetMessageID] = resolved
	}
}

// dropPromptLocked removes a prompt registration.
// Must be called with f.mu held.
func (f *ApprovalFlow[D]) dropPromptLocked(approvalID string) {
	approvalID = strings.TrimSpace(approvalID)
	if approvalID == "" {
		return
	}
	entry := f.promptsByApproval[approvalID]
	if entry != nil && entry.PromptMessageID != "" {
		delete(f.promptsByMsgID, entry.PromptMessageID)
	}
	if entry != nil && entry.ReactionTargetMessageID != "" {
		delete(f.reactionTargetsByMsgID, entry.ReactionTargetMessageID)
	}
	delete(f.promptsByApproval, approvalID)
}

func (f *ApprovalFlow[D]) matchReactionTarget(targetMessageID networkid.MessageID, sender id.UserID, key string, now time.Time) ApprovalPromptReactionMatch {
	targetMessageID = networkid.MessageID(strings.TrimSpace(string(targetMessageID)))
	key = normalizeReactionKey(key)
	if targetMessageID == "" || key == "" {
		return ApprovalPromptReactionMatch{}
	}

	f.mu.Lock()
	approvalID := f.promptsByMsgID[targetMessageID]
	if approvalID == "" {
		approvalID = f.reactionTargetsByMsgID[targetMessageID]
	}
	entry := f.promptsByApproval[approvalID]
	if entry == nil {
		f.mu.Unlock()
		return ApprovalPromptReactionMatch{}
	}
	promptCopy := *entry
	f.mu.Unlock()

	sender = id.UserID(strings.TrimSpace(sender.String()))

	match := ApprovalPromptReactionMatch{
		KnownPrompt: true,
		ApprovalID:  approvalID,
		Prompt:      promptCopy,
	}
	if promptCopy.OwnerMXID != "" && sender != promptCopy.OwnerMXID {
		match.RejectReason = RejectReasonOwnerOnly
		return match
	}
	if !promptCopy.ExpiresAt.IsZero() && !now.IsZero() && now.After(promptCopy.ExpiresAt) {
		match.RejectReason = RejectReasonExpired
		f.mu.Lock()
		f.dropPromptLocked(approvalID)
		f.mu.Unlock()
		return match
	}
	for _, opt := range promptCopy.Options {
		for _, optKey := range opt.allKeys() {
			if key != optKey {
				continue
			}
			match.ShouldResolve = true
			match.Decision = ApprovalDecisionPayload{
				ApprovalID:  promptCopy.ApprovalID,
				Approved:    opt.Approved,
				Always:      opt.Always,
				Reason:      opt.decisionReason(),
				ReactionKey: key,
				ResolvedBy:  ApprovalResolutionOriginUser,
			}
			return match
		}
	}
	match.RejectReason = RejectReasonInvalidOption
	return match
}

// scanPromptsByRoom iterates promptsByApproval under f.mu, filtering for
// entries in the given room that have a pending approval and match the sender
// (or have no owner restriction). Expired prompts are dropped automatically.
// The visit callback is called for each live match and receives the approvalID
// and a copy of the entry; returning false stops the scan early.
//
// Locking: acquires and releases f.mu internally. The visit callback runs
// under f.mu — it must not call methods that acquire the lock.
func (f *ApprovalFlow[D]) scanPromptsByRoom(roomID id.RoomID, sender id.UserID, now time.Time, visit func(approvalID string, entry ApprovalPromptRegistration) bool) {
	var expiredIDs []string

	f.mu.Lock()
	for approvalID, entry := range f.promptsByApproval {
		if entry == nil || entry.RoomID != roomID {
			continue
		}
		if _, ok := f.pending[approvalID]; !ok {
			continue
		}
		if entry.OwnerMXID != "" && sender != entry.OwnerMXID {
			continue
		}
		if !entry.ExpiresAt.IsZero() && !now.IsZero() && now.After(entry.ExpiresAt) {
			expiredIDs = append(expiredIDs, approvalID)
			continue
		}
		if !visit(approvalID, *entry) {
			break
		}
	}
	for _, approvalID := range expiredIDs {
		f.dropPromptLocked(approvalID)
	}
	f.mu.Unlock()
}

func (f *ApprovalFlow[D]) matchFallbackReaction(roomID id.RoomID, sender id.UserID, key string, now time.Time) ApprovalPromptReactionMatch {
	roomID = id.RoomID(strings.TrimSpace(roomID.String()))
	sender = id.UserID(strings.TrimSpace(sender.String()))
	key = normalizeReactionKey(key)
	if roomID == "" || sender == "" || key == "" {
		return ApprovalPromptReactionMatch{}
	}

	var (
		found int
		match ApprovalPromptReactionMatch
	)

	f.scanPromptsByRoom(roomID, sender, now, func(approvalID string, entry ApprovalPromptRegistration) bool {
		var decision ApprovalDecisionPayload
		matched := false
		for _, opt := range entry.Options {
			for _, optKey := range opt.allKeys() {
				if key != optKey {
					continue
				}
				matched = true
				decision = ApprovalDecisionPayload{
					ApprovalID:  entry.ApprovalID,
					Approved:    opt.Approved,
					Always:      opt.Always,
					Reason:      opt.decisionReason(),
					ReactionKey: key,
					ResolvedBy:  ApprovalResolutionOriginUser,
				}
				break
			}
			if matched {
				break
			}
		}
		if !matched {
			return true // continue scanning
		}

		found++
		if found > 1 {
			match = ApprovalPromptReactionMatch{}
			return false // stop scanning
		}
		match = ApprovalPromptReactionMatch{
			KnownPrompt:            true,
			ShouldResolve:          true,
			ApprovalID:             approvalID,
			Decision:               decision,
			Prompt:                 entry,
			MirrorDecisionReaction: true,
			RedactResolvedReaction: true,
		}
		return true // continue scanning to check for ambiguity
	})

	if found == 1 {
		return match
	}
	return ApprovalPromptReactionMatch{}
}

func (f *ApprovalFlow[D]) hasPendingApprovalForOwner(roomID id.RoomID, sender id.UserID, now time.Time) bool {
	roomID = id.RoomID(strings.TrimSpace(roomID.String()))
	sender = id.UserID(strings.TrimSpace(sender.String()))
	if roomID == "" || sender == "" {
		return false
	}

	hasPending := false
	f.scanPromptsByRoom(roomID, sender, now, func(_ string, _ ApprovalPromptRegistration) bool {
		hasPending = true
		return false // stop scanning, one match is enough
	})
	return hasPending
}

// SendPromptParams holds the parameters for sending an approval prompt.
type SendPromptParams struct {
	ApprovalPromptMessageParams
	RoomID    id.RoomID
	OwnerMXID id.UserID
}

// ---------------------------------------------------------------------------
// Prompt sending
// ---------------------------------------------------------------------------

// SendPrompt builds an approval prompt message, registers it in the prompt
// store, sends it via the configured sender, binds the prompt identifiers, and
// queues prefill reactions.
func (f *ApprovalFlow[D]) SendPrompt(ctx context.Context, portal *bridgev2.Portal, params SendPromptParams) {
	if f == nil || portal == nil || portal.MXID == "" {
		return
	}
	f.ensureReaperRunning()
	login := f.loginOrNil()
	if login == nil {
		return
	}
	approvalID := strings.TrimSpace(params.ApprovalID)
	if approvalID == "" {
		return
	}

	prompt := BuildApprovalPromptMessage(params.ApprovalPromptMessageParams)
	sender := f.senderOrEmpty(portal)
	reactionTargetMessageID := resolveApprovalReactionTargetMessageID(ctx, login, params.ReplyToEventID)

	f.mu.Lock()
	var prevPromptCopy ApprovalPromptRegistration
	hadPrevPrompt := false
	if prev := f.promptsByApproval[approvalID]; prev != nil {
		prevPromptCopy = *prev
		hadPrevPrompt = true
	}
	f.registerPromptLocked(ApprovalPromptRegistration{
		ApprovalID:              approvalID,
		RoomID:                  params.RoomID,
		OwnerMXID:               params.OwnerMXID,
		ToolCallID:              strings.TrimSpace(params.ToolCallID),
		ToolName:                strings.TrimSpace(params.ToolName),
		TurnID:                  strings.TrimSpace(params.TurnID),
		Presentation:            prompt.Presentation,
		ExpiresAt:               params.ExpiresAt,
		Options:                 prompt.Options,
		ReactionTargetMessageID: reactionTargetMessageID,
		PromptSenderID:          sender.Sender,
	})
	f.mu.Unlock()

	var dbMeta any
	if f.dbMetadata != nil {
		dbMeta = f.dbMetadata(prompt)
	} else {
		dbMeta = &BaseMessageMetadata{
			Role:               "assistant",
			ExcludeFromHistory: true,
		}
	}

	converted := &bridgev2.ConvertedMessage{
		Parts: []*bridgev2.ConvertedMessagePart{{
			ID:         networkid.PartID("0"),
			Type:       event.EventMessage,
			Content:    prompt.Content,
			Extra:      prompt.TopLevelExtra,
			DBMetadata: dbMeta,
		}},
	}

	_, msgID, err := f.send(ctx, portal, converted)
	if err != nil {
		f.mu.Lock()
		f.dropPromptLocked(approvalID)
		if hadPrevPrompt {
			f.registerPromptLocked(prevPromptCopy)
		}
		f.mu.Unlock()
		return
	}

	f.mu.Lock()
	_, bound := f.bindPromptTargetLocked(approvalID, msgID)
	if !bound {
		f.dropPromptLocked(approvalID)
		if hadPrevPrompt {
			f.registerPromptLocked(prevPromptCopy)
		}
	}
	f.mu.Unlock()
	if !bound {
		loggerForLogin(ctx, login).Warn().
			Str("approval_msg_id", string(msgID)).
			Str("approval_id", approvalID).
			Msg("Failed to bind approval prompt message ID")
		return
	}

	f.sendPrefillReactions(ctx, portal, login, approvalReactionTargetMessageID(ApprovalPromptRegistration{
		ReactionTargetMessageID: reactionTargetMessageID,
		PromptMessageID:         msgID,
	}), prompt.Options)
	f.schedulePromptTimeout(approvalID, params.ExpiresAt)
}

// ---------------------------------------------------------------------------
// Reaction handling (satisfies ApprovalReactionHandler)
// ---------------------------------------------------------------------------

// HandleReaction checks whether a reaction targets a known approval prompt.
// If so, it validates room, resolves the approval (via channel or DeliverDecision),
// and redacts prompt reactions.
func (f *ApprovalFlow[D]) HandleReaction(ctx context.Context, msg *bridgev2.MatrixReaction) bool {
	if f == nil || msg == nil || msg.Event == nil || msg.Portal == nil {
		return false
	}
	now := time.Now()
	rc := ExtractReactionContext(msg)
	targetMessageID := rc.TargetMessageID
	match := f.matchReactionTarget(targetMessageID, msg.Event.Sender, rc.Emoji, now)
	if !match.KnownPrompt && targetMessageID == "" && rc.TargetEventID != "" {
		targetMessageID = networkid.MessageID(strings.TrimSpace(rc.TargetEventID.String()))
		match = f.matchReactionTarget(targetMessageID, msg.Event.Sender, rc.Emoji, now)
	}
	if !match.KnownPrompt {
		if isApprovalReactionKey(rc.Emoji) && f.handleResolvedApprovalReactionChange(ctx, msg.Portal, msg.Event, msg, targetMessageID) {
			return true
		}
		match = f.matchFallbackReaction(msg.Portal.MXID, msg.Event.Sender, rc.Emoji, now)
		if !match.KnownPrompt {
			if isApprovalReactionKey(rc.Emoji) && f.hasPendingApprovalForOwner(msg.Portal.MXID, msg.Event.Sender, now) {
				f.sendMessageStatus(ctx, msg.Portal, msg.Event, bridgev2.MessageStatus{
					Status:      event.MessageStatusFail,
					ErrorReason: event.MessageStatusGenericError,
					Message:     approvalWrongTargetMSSMessage,
					IsCertain:   true,
				})
				f.redactSingleReaction(msg)
				return true
			}
			return false
		}
	}

	if !match.ShouldResolve {
		f.handleRejectedReaction(ctx, msg, match)
		return true
	}

	// Look up pending approval and validate room.
	approvalID := strings.TrimSpace(match.ApprovalID)
	f.mu.Lock()
	p := f.pending[approvalID]
	f.mu.Unlock()

	if p != nil && !p.ExpiresAt.IsZero() && now.After(p.ExpiresAt) {
		f.finishTimedOutApproval(approvalID)
		if f.sendNotice != nil {
			f.sendNotice(ctx, msg.Portal, ApprovalErrorToastText(ErrApprovalExpired))
		}
		f.redactSingleReaction(msg)
		return true
	}
	if p != nil && f.roomIDFromData != nil {
		dataRoomID := f.roomIDFromData(p.Data)
		if dataRoomID != "" && dataRoomID != msg.Portal.MXID {
			if f.sendNotice != nil {
				f.sendNotice(ctx, msg.Portal, ApprovalErrorToastText(ErrApprovalWrongRoom))
			}
			f.redactSingleReaction(msg)
			return true
		}
	}
	if p == nil {
		if f.sendNotice != nil {
			f.sendNotice(ctx, msg.Portal, ApprovalErrorToastText(ErrApprovalUnknown))
		}
		f.redactSingleReaction(msg)
		return true
	}

	resolved := false
	if f.deliverDecision != nil {
		// Callback-based flow (OpenCode/OpenClaw).
		if err := f.deliverDecision(ctx, msg.Portal, p, match.Decision); err != nil {
			if f.sendNotice != nil {
				f.sendNotice(ctx, msg.Portal, ApprovalErrorToastText(err))
			}
			f.redactSingleReaction(msg)
		} else {
			resolved = true
		}
	} else {
		// Channel-based flow (Codex).
		select {
		case p.ch <- match.Decision:
			resolved = true
		default:
			if f.sendNotice != nil {
				f.sendNotice(ctx, msg.Portal, ApprovalErrorToastText(ErrApprovalAlreadyHandled))
			}
		}
	}

	if resolved {
		if match.RedactResolvedReaction {
			f.redactSingleReaction(msg)
		}
		if match.MirrorDecisionReaction {
			f.mirrorRemoteDecisionReaction(ctx, match.Prompt, match.Decision)
		}
		f.FinishResolved(approvalID, match.Decision)
	}
	return true
}

// HandleReactionRemove rejects post-resolution approval reaction removals so the
// chosen terminal action stays immutable.
func (f *ApprovalFlow[D]) HandleReactionRemove(ctx context.Context, msg *bridgev2.MatrixReactionRemove) bool {
	if f == nil || msg == nil || msg.Event == nil || msg.Portal == nil || msg.TargetReaction == nil {
		return false
	}
	emoji := msg.TargetReaction.Emoji
	if emoji == "" {
		emoji = string(msg.TargetReaction.EmojiID)
	}
	if !isApprovalReactionKey(emoji) {
		return false
	}
	return f.handleResolvedApprovalReactionChange(ctx, msg.Portal, msg.Event, nil, msg.TargetReaction.MessageID)
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

func (f *ApprovalFlow[D]) handleRejectedReaction(ctx context.Context, msg *bridgev2.MatrixReaction, match ApprovalPromptReactionMatch) {
	if f.sendNotice != nil {
		switch match.RejectReason {
		case RejectReasonExpired:
			f.sendNotice(ctx, msg.Portal, ApprovalErrorToastText(ErrApprovalExpired))
		case RejectReasonOwnerOnly:
			f.sendNotice(ctx, msg.Portal, ApprovalErrorToastText(ErrApprovalOnlyOwner))
		}
	}
	f.redactSingleReaction(msg)
}

func (f *ApprovalFlow[D]) handleResolvedApprovalReactionChange(
	ctx context.Context,
	portal *bridgev2.Portal,
	evt *event.Event,
	reaction *bridgev2.MatrixReaction,
	targetMessageID networkid.MessageID,
) bool {
	if portal == nil || evt == nil {
		return false
	}
	if _, ok := f.resolvedPromptByTarget(targetMessageID); !ok {
		return false
	}
	f.sendMessageStatus(ctx, portal, evt, bridgev2.MessageStatus{
		Status:      event.MessageStatusFail,
		ErrorReason: event.MessageStatusGenericError,
		Message:     approvalResolvedMSSMessage,
		IsCertain:   true,
	})
	if reaction != nil {
		f.redactSingleReaction(reaction)
	}
	return true
}

func resolveApprovalReactionTargetMessageID(
	ctx context.Context,
	login *bridgev2.UserLogin,
	replyToEventID id.EventID,
) networkid.MessageID {
	replyToEventID = id.EventID(strings.TrimSpace(replyToEventID.String()))
	if login == nil || login.Bridge == nil || replyToEventID == "" {
		return ""
	}
	msg, err := login.Bridge.DB.Message.GetPartByMXID(ctx, replyToEventID)
	if err != nil || msg == nil {
		return ""
	}
	return msg.ID
}

// resolvePromptTargetMessage returns the remote message ID for a prompt,
// trying the supplied primaryID first, then falling back to a database
// lookup via resolveApprovalPromptMessage.
func resolvePromptTargetMessage(
	ctx context.Context,
	login *bridgev2.UserLogin,
	portal *bridgev2.Portal,
	prompt ApprovalPromptRegistration,
	primaryID networkid.MessageID,
) networkid.MessageID {
	if primaryID != "" {
		return primaryID
	}
	receiver := portal.Receiver
	if receiver == "" {
		receiver = login.ID
	}
	target := resolveApprovalPromptMessage(ctx, login, receiver, prompt)
	if target == nil {
		return ""
	}
	return target.ID
}

func approvalReactionTargetMessageID(prompt ApprovalPromptRegistration) networkid.MessageID {
	if prompt.ReactionTargetMessageID != "" {
		return prompt.ReactionTargetMessageID
	}
	return prompt.PromptMessageID
}

func (f *ApprovalFlow[D]) redactSingleReaction(msg *bridgev2.MatrixReaction) {
	if f.testRedactSingleReaction != nil {
		f.testRedactSingleReaction(msg)
		return
	}
	login := f.loginOrNil()
	sender := f.reactionRedactionSender(msg)
	triggerID := msg.Event.ID
	portal := msg.Portal
	go func() {
		ctx := context.Background()
		if f.backgroundCtx != nil {
			ctx = f.backgroundCtx(ctx)
		}
		if msg != nil && msg.Event != nil && msg.Event.Sender != "" {
			_ = EnsureSyntheticReactionSenderGhost(ctx, login, msg.Event.Sender)
		}
		_ = RedactEventAsSender(ctx, login, portal, sender, triggerID)
	}()
}

func (f *ApprovalFlow[D]) reactionRedactionSender(msg *bridgev2.MatrixReaction) bridgev2.EventSender {
	if msg != nil && msg.Event != nil && msg.Event.Sender != "" {
		return bridgev2.EventSender{
			Sender: MatrixSenderID(msg.Event.Sender),
			SenderLogin: func() networkid.UserLoginID {
				if login := f.loginOrNil(); login != nil {
					return login.ID
				}
				return ""
			}(),
		}
	}
	if msg != nil {
		return f.senderOrEmpty(msg.Portal)
	}
	return bridgev2.EventSender{}
}

func (f *ApprovalFlow[D]) sendMessageStatus(ctx context.Context, portal *bridgev2.Portal, evt *event.Event, status bridgev2.MessageStatus) {
	if f.testSendMessageStatus != nil {
		f.testSendMessageStatus(ctx, portal, evt, status)
		return
	}
	SendMatrixMessageStatus(ctx, portal, evt, status)
}

func (f *ApprovalFlow[D]) senderOrEmpty(portal *bridgev2.Portal) bridgev2.EventSender {
	if f.sender != nil {
		return f.sender(portal)
	}
	return bridgev2.EventSender{}
}

func (f *ApprovalFlow[D]) loginOrNil() *bridgev2.UserLogin {
	if f == nil || f.login == nil {
		return nil
	}
	return f.login()
}

func (f *ApprovalFlow[D]) send(_ context.Context, portal *bridgev2.Portal, converted *bridgev2.ConvertedMessage) (id.EventID, networkid.MessageID, error) {
	login := f.loginOrNil()
	if login == nil {
		return "", "", nil
	}
	return SendViaPortal(SendViaPortalParams{
		Login:     login,
		Portal:    portal,
		Sender:    f.senderOrEmpty(portal),
		IDPrefix:  f.idPrefix,
		LogKey:    f.logKey,
		Converted: converted,
	})
}

func (f *ApprovalFlow[D]) sendPrefillReactions(ctx context.Context, portal *bridgev2.Portal, login *bridgev2.UserLogin, targetMessageID networkid.MessageID, options []ApprovalOption) {
	if login == nil || portal == nil || targetMessageID == "" {
		return
	}
	sender := f.senderOrEmpty(portal)
	logger := loggerForLogin(ctx, login)
	now := time.Now()
	seen := map[string]struct{}{}
	for _, option := range options {
		key := approvalPlaceholderReactionKey(option)
		if key == "" {
			continue
		}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		result := login.QueueRemoteEvent(BuildReactionEvent(
			portal.PortalKey,
			sender,
			targetMessageID,
			key,
			networkid.EmojiID(key),
			now,
			0,
			f.logKey,
			nil,
			nil,
		))
		if !result.Success {
			logEvt := logger.Warn().
				Str("approval_reaction_key", key).
				Str("approval_reaction_target_msg_id", string(targetMessageID)).
				Str("reaction_sender", string(sender.Sender))
			if result.Error != nil {
				logEvt = logEvt.Err(result.Error)
			}
			logEvt.Msg("Failed to queue approval placeholder reaction")
			continue
		}
		logger.Debug().
			Str("approval_reaction_key", key).
			Str("approval_reaction_target_msg_id", string(targetMessageID)).
			Str("reaction_sender", string(sender.Sender)).
			Msg("Queued approval placeholder reaction")
	}
}

func (f *ApprovalFlow[D]) schedulePromptTimeout(approvalID string, expiresAt time.Time) {
	f.ensureReaperRunning()
	approvalID = strings.TrimSpace(approvalID)
	if approvalID == "" || expiresAt.IsZero() {
		return
	}
	if time.Until(expiresAt) <= 0 {
		f.finishTimedOutApproval(approvalID)
		return
	}
	// Wake the reaper so it picks up the new expiry promptly.
	f.wakeReaper()
}

func (f *ApprovalFlow[D]) finishTimedOutApproval(approvalID string) {
	f.finishTimedOutApprovalWithPromptVersion(approvalID, 0)
}

func (f *ApprovalFlow[D]) finishTimedOutApprovalWithPromptVersion(approvalID string, promptVersion uint64) {
	f.finalizeWithPromptVersion(approvalID, &ApprovalDecisionPayload{
		ApprovalID: approvalID,
		Reason:     ApprovalReasonTimeout,
	}, true, promptVersion)
}

func (f *ApprovalFlow[D]) cancelPendingTimeout(approvalID string) {
	approvalID = strings.TrimSpace(approvalID)
	if approvalID == "" {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if p := f.pending[approvalID]; p != nil {
		p.closeDone()
	}
}

func approvalOptionDecisionKey(option ApprovalOption) string {
	if option.Key != "" {
		return option.Key
	}
	return option.FallbackKey
}

func approvalOptionKeyForDecision(options []ApprovalOption, decision ApprovalDecisionPayload) string {
	options = normalizeApprovalOptions(options, DefaultApprovalOptions())
	if decision.Approved {
		if decision.Always {
			for _, option := range options {
				if option.Approved && option.Always {
					return approvalOptionDecisionKey(option)
				}
			}
		}
		for _, option := range options {
			if option.Approved && !option.Always {
				return approvalOptionDecisionKey(option)
			}
		}
		return ""
	}
	switch strings.TrimSpace(decision.Reason) {
	case ApprovalReasonTimeout, ApprovalReasonExpired, ApprovalReasonDeliveryError, ApprovalReasonCancelled:
		return ""
	}
	for _, option := range options {
		if !option.Approved {
			return approvalOptionDecisionKey(option)
		}
	}
	return ""
}

func approvalPlaceholderReactionKey(option ApprovalOption) string {
	if key := normalizeReactionKey(option.FallbackKey); key != "" {
		return key
	}
	return normalizeReactionKey(option.Key)
}

func approvalReactionKeyForDecision(options []ApprovalOption, decision ApprovalDecisionPayload) string {
	canonicalKey := approvalOptionKeyForDecision(options, decision)
	if canonicalKey == "" {
		return ""
	}
	if normalizeApprovalResolutionOrigin(decision.ResolvedBy) != ApprovalResolutionOriginUser {
		return canonicalKey
	}
	reactionKey := normalizeReactionKey(decision.ReactionKey)
	if reactionKey == "" {
		return canonicalKey
	}
	for _, option := range normalizeApprovalOptions(options, DefaultApprovalOptions()) {
		if option.Key != canonicalKey {
			continue
		}
		for _, optionKey := range option.allKeys() {
			if reactionKey == optionKey {
				return reactionKey
			}
		}
		break
	}
	return canonicalKey
}

func approvalCleanupOptions(prompt ApprovalPromptRegistration, decision *ApprovalDecisionPayload, sender bridgev2.EventSender) ApprovalPromptReactionCleanupOptions {
	if decision == nil || normalizeApprovalResolutionOrigin(decision.ResolvedBy) != ApprovalResolutionOriginAgent {
		return ApprovalPromptReactionCleanupOptions{}
	}
	reactionKey := approvalOptionKeyForDecision(prompt.Options, *decision)
	if reactionKey == "" {
		return ApprovalPromptReactionCleanupOptions{}
	}
	return ApprovalPromptReactionCleanupOptions{
		PreserveSenderID: approvalPromptPlaceholderSenderID(prompt, sender),
		PreserveKey:      reactionKey,
	}
}

func (f *ApprovalFlow[D]) mirrorRemoteDecisionReaction(ctx context.Context, prompt ApprovalPromptRegistration, decision ApprovalDecisionPayload) {
	if normalizeApprovalResolutionOrigin(decision.ResolvedBy) != ApprovalResolutionOriginUser {
		return
	}
	reactionKey := approvalReactionKeyForDecision(prompt.Options, decision)
	if reactionKey == "" {
		return
	}
	login := f.loginOrNil()
	if login == nil || login.Bridge == nil {
		return
	}
	portal, err := f.resolvePortalByRoomID(ctx, login, prompt.RoomID)
	if err != nil || portal == nil || portal.MXID == "" {
		return
	}
	sender := bridgev2.EventSender{Sender: MatrixSenderID(prompt.OwnerMXID), SenderLogin: login.ID}
	if f.testMirrorRemoteDecisionReaction != nil {
		f.testMirrorRemoteDecisionReaction(ctx, login, portal, sender, prompt, reactionKey)
		return
	}
	if prompt.OwnerMXID != "" {
		_ = EnsureSyntheticReactionSenderGhost(ctx, login, prompt.OwnerMXID)
	}
	targetMessage := resolvePromptTargetMessage(ctx, login, portal, prompt, approvalReactionTargetMessageID(prompt))
	if targetMessage == "" {
		return
	}
	login.QueueRemoteEvent(BuildReactionEvent(
		portal.PortalKey,
		sender,
		targetMessage,
		reactionKey,
		networkid.EmojiID(reactionKey),
		time.Now(),
		0,
		f.logKey,
		nil,
		nil,
	))
}

func (f *ApprovalFlow[D]) finalizeWithPromptVersion(approvalID string, decision *ApprovalDecisionPayload, resolved bool, promptVersion uint64) bool {
	approvalID = strings.TrimSpace(approvalID)
	if approvalID == "" {
		return false
	}
	var prompt *ApprovalPromptRegistration
	f.mu.Lock()
	if promptVersion != 0 {
		entry := f.promptsByApproval[approvalID]
		if entry == nil || entry.PromptVersion != promptVersion {
			f.mu.Unlock()
			return false
		}
	}
	if p := f.pending[approvalID]; p != nil {
		p.closeDone()
	}
	delete(f.pending, approvalID)
	if entry := f.promptsByApproval[approvalID]; entry != nil {
		copyEntry := *entry
		prompt = &copyEntry
	}
	if prompt != nil && resolved && decision != nil {
		f.rememberResolvedPromptLocked(*prompt, *decision)
	}
	f.dropPromptLocked(approvalID)
	f.mu.Unlock()
	if prompt == nil {
		return true
	}
	login := f.loginOrNil()
	if login == nil || login.Bridge == nil {
		return true
	}
	go func(prompt ApprovalPromptRegistration, decision *ApprovalDecisionPayload, resolved bool) {
		ctx := context.Background()
		if f.backgroundCtx != nil {
			ctx = f.backgroundCtx(ctx)
		}
		portal, err := f.resolvePortalByRoomID(ctx, login, prompt.RoomID)
		if err != nil || portal == nil || portal.MXID == "" {
			return
		}
		sender := f.senderOrEmpty(portal)
		if prompt.PromptSenderID != "" {
			sender.Sender = prompt.PromptSenderID
		}
		ac := approvalContext{ctx: ctx, login: login, portal: portal, sender: sender}
		cleanupOpts := approvalCleanupOptions(prompt, decision, sender)
		if resolved && decision != nil {
			if f.testEditPromptToResolvedState != nil {
				f.testEditPromptToResolvedState(ctx, login, portal, sender, prompt, *decision)
			} else {
				f.editPromptToResolvedState(ac, prompt, *decision)
			}
		}
		if f.testRedactPromptPlaceholderReacts != nil {
			_ = f.testRedactPromptPlaceholderReacts(ctx, login, portal, sender, prompt, cleanupOpts)
			return
		}
		_ = RedactApprovalPromptPlaceholderReactions(ac.ctx, ac.login, ac.portal, ac.sender, prompt, cleanupOpts)
	}(*prompt, decision, resolved)
	return true
}

// approvalContext bundles the four values that are always passed together
// through the approval resolution path.
type approvalContext struct {
	ctx    context.Context
	login  *bridgev2.UserLogin
	portal *bridgev2.Portal
	sender bridgev2.EventSender
}

func (f *ApprovalFlow[D]) resolvePortalByRoomID(ctx context.Context, login *bridgev2.UserLogin, roomID id.RoomID) (*bridgev2.Portal, error) {
	if f.testResolvePortal != nil {
		return f.testResolvePortal(ctx, login, roomID)
	}
	return login.Bridge.GetPortalByMXID(ctx, roomID)
}

func (f *ApprovalFlow[D]) editPromptToResolvedState(
	ac approvalContext,
	prompt ApprovalPromptRegistration,
	decision ApprovalDecisionPayload,
) {
	if ac.login == nil || ac.portal == nil || ac.portal.MXID == "" {
		return
	}
	targetMessage := resolvePromptTargetMessage(ac.ctx, ac.login, ac.portal, prompt, prompt.PromptMessageID)
	if targetMessage == "" {
		return
	}
	response := BuildApprovalResponsePromptMessage(ApprovalResponsePromptMessageParams{
		ApprovalID:   prompt.ApprovalID,
		ToolCallID:   prompt.ToolCallID,
		ToolName:     prompt.ToolName,
		TurnID:       prompt.TurnID,
		Presentation: prompt.Presentation,
		Options:      prompt.Options,
		Decision:     decision,
		ExpiresAt:    prompt.ExpiresAt,
	})
	if response.Content == nil {
		return
	}
	edit := &bridgev2.ConvertedEdit{
		ModifiedParts: []*bridgev2.ConvertedEditPart{{
			Type:          event.EventMessage,
			Content:       response.Content,
			TopLevelExtra: response.TopLevelExtra,
		}},
	}
	ac.login.QueueRemoteEvent(&RemoteEdit{
		Portal:        ac.portal.PortalKey,
		Sender:        ac.sender,
		TargetMessage: targetMessage,
		Timestamp:     time.Now(),
		PreBuilt:      edit,
		LogKey:        f.logKey,
	})
}
