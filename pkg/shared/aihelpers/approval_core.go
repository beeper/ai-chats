package aihelpers

import (
	"context"
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
	Login func() *bridgev2.UserLogin

	// Sender returns the EventSender to use for a given portal (e.g. the agent ghost).
	Sender func(portal *bridgev2.Portal) bridgev2.EventSender

	// BackgroundContext optionally returns a context detached from the request lifecycle.
	BackgroundContext func(ctx context.Context) context.Context

	// RoomIDFromData extracts the stored room ID from pending data for validation.
	// Return "" to skip the room check.
	RoomIDFromData func(data D) id.RoomID

	// DeliverDecision is called for non-channel flows when a valid reaction resolves
	// an approval. If nil, the flow is channel-based and decisions are retrieved with Wait.
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

	reaperStop   chan struct{}
	reaperNotify chan struct{}

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
