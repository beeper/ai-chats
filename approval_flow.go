package agentremote

import (
	"context"
	"strings"
	"sync"
	"time"

	"maunium.net/go/mautrix/bridgev2"

	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/turns"
)

// ApprovalReactionHandler is the interface used by BaseReactionHandler to
// dispatch reactions to the approval system without knowing the concrete type.
type ApprovalReactionHandler interface {
	HandleReaction(ctx context.Context, msg *bridgev2.MatrixReaction, targetEventID id.EventID, emoji string) bool
}

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

// ApprovalFlow owns the full lifecycle of approval prompts and pending approvals.
// D is the bridge-specific pending data type.
type ApprovalFlow[D any] struct {
	mu      sync.Mutex
	pending map[string]*Pending[D]

	// Prompt store (inlined from ApprovalPromptStore).
	promptsByApproval map[string]*ApprovalPromptRegistration
	promptsByEventID  map[id.EventID]string

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
	testRedactPromptPlaceholderReacts func(ctx context.Context, login *bridgev2.UserLogin, portal *bridgev2.Portal, sender bridgev2.EventSender, prompt ApprovalPromptRegistration) error
	testMirrorRemoteDecisionReaction  func(ctx context.Context, login *bridgev2.UserLogin, portal *bridgev2.Portal, sender bridgev2.EventSender, prompt ApprovalPromptRegistration, reactionKey string)
	testRedactSingleReaction          func(msg *bridgev2.MatrixReaction)
}

// NewApprovalFlow creates an ApprovalFlow from the given config.
// Call Close() when the flow is no longer needed to stop the reaper goroutine.
func NewApprovalFlow[D any](cfg ApprovalFlowConfig[D]) *ApprovalFlow[D] {
	timeout := cfg.SendTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	f := &ApprovalFlow[D]{
		pending:           make(map[string]*Pending[D]),
		promptsByApproval: make(map[string]*ApprovalPromptRegistration),
		promptsByEventID:  make(map[id.EventID]string),
		login:             cfg.Login,
		sender:            cfg.Sender,
		backgroundCtx:     cfg.BackgroundContext,
		roomIDFromData:    cfg.RoomIDFromData,
		deliverDecision:   cfg.DeliverDecision,
		sendNotice:        cfg.SendNotice,
		dbMetadata:        cfg.DBMetadata,
		idPrefix:          cfg.IDPrefix,
		logKey:            cfg.LogKey,
		sendTimeout:       timeout,
		reaperStop:        make(chan struct{}),
		reaperNotify:      make(chan struct{}, 1),
	}
	go f.runReaper()
	return f
}

// Close stops the reaper goroutine. Safe to call multiple times.
func (f *ApprovalFlow[D]) Close() {
	if f == nil {
		return
	}
	select {
	case <-f.reaperStop:
	default:
		close(f.reaperStop)
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

// nextReaperDelay returns the duration until the earliest pending/prompt expiry,
// capped at reaperMaxInterval.
func (f *ApprovalFlow[D]) nextReaperDelay() time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	earliest := time.Time{}
	for _, p := range f.pending {
		if !p.ExpiresAt.IsZero() && (earliest.IsZero() || p.ExpiresAt.Before(earliest)) {
			earliest = p.ExpiresAt
		}
	}
	for _, entry := range f.promptsByApproval {
		if !entry.ExpiresAt.IsZero() && (earliest.IsZero() || entry.ExpiresAt.Before(earliest)) {
			earliest = entry.ExpiresAt
		}
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
	var expired []string
	f.mu.Lock()
	// Finalize pending approvals whose own TTL has elapsed.
	for aid, p := range f.pending {
		if !p.ExpiresAt.IsZero() && now.After(p.ExpiresAt) {
			expired = append(expired, aid)
		}
	}
	// Also finalize pending approvals whose associated prompt has expired.
	for aid, entry := range f.promptsByApproval {
		if !entry.ExpiresAt.IsZero() && now.After(entry.ExpiresAt) {
			if _, hasPending := f.pending[aid]; hasPending {
				expired = append(expired, aid)
			} else {
				// Orphan prompt — clean it up.
				if entry.PromptEventID != "" {
					delete(f.promptsByEventID, entry.PromptEventID)
				}
				delete(f.promptsByApproval, aid)
			}
		}
	}
	f.mu.Unlock()
	for _, aid := range expired {
		f.finishTimedOutApproval(aid, 0)
	}
}

// ---------------------------------------------------------------------------
// Pending approval store
// ---------------------------------------------------------------------------

// Register adds a new pending approval with the given TTL and bridge-specific data.
// Returns the Pending and true if newly created, or the existing one and false
// if a non-expired approval with the same ID already exists.
func (f *ApprovalFlow[D]) Register(approvalID string, ttl time.Duration, data D) (*Pending[D], bool) {
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
	f.finalize(approvalID, nil, false)
}

// FinishResolved finalizes a terminal approval by editing the approval prompt to
// its final state and cleaning up bridge-authored placeholder reactions.
func (f *ApprovalFlow[D]) FinishResolved(approvalID string, decision ApprovalDecisionPayload) {
	if f == nil {
		return
	}
	approvalID = strings.TrimSpace(approvalID)
	if approvalID == "" {
		return
	}
	if strings.TrimSpace(decision.ApprovalID) == "" {
		decision.ApprovalID = approvalID
	}
	f.finalize(approvalID, &decision, true)
}

// ResolveExternal mirrors a concrete remote allow/deny decision into Matrix as
// an owner-authored reaction when possible, then finalizes the approval if the
// decision was accepted by the internal delivery path.
func (f *ApprovalFlow[D]) ResolveExternal(ctx context.Context, approvalID string, decision ApprovalDecisionPayload) {
	if f == nil {
		return
	}
	approvalID = strings.TrimSpace(approvalID)
	if approvalID == "" {
		return
	}
	if strings.TrimSpace(decision.ApprovalID) == "" {
		decision.ApprovalID = approvalID
	}
	if prompt, ok := f.promptRegistration(approvalID); ok {
		f.mirrorRemoteDecisionReaction(ctx, prompt, decision)
	}
	if err := f.Resolve(approvalID, decision); err != nil {
		return
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
		f.finishTimedOutApproval(approvalID, 0)
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
	timeout := time.Until(p.ExpiresAt)
	if timeout <= 0 {
		f.finishTimedOutApproval(approvalID, 0)
		return zero, false
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case d := <-p.ch:
		return d, true
	case <-timer.C:
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
	if prev != nil && prev.PromptEventID != "" {
		delete(f.promptsByEventID, prev.PromptEventID)
	}
	copyReg := reg
	f.promptsByApproval[reg.ApprovalID] = &copyReg
	if reg.PromptEventID != "" {
		f.promptsByEventID[reg.PromptEventID] = reg.ApprovalID
	}
}

// bindPromptEventLocked associates an event ID with a prompt registration and
// returns the prompt generation that should own any timeout goroutine.
// Must be called with f.mu held.
func (f *ApprovalFlow[D]) bindPromptIDsLocked(approvalID string, eventID id.EventID, messageID networkid.MessageID) (uint64, bool) {
	approvalID = strings.TrimSpace(approvalID)
	eventID = id.EventID(strings.TrimSpace(eventID.String()))
	messageID = networkid.MessageID(strings.TrimSpace(string(messageID)))
	if approvalID == "" || eventID == "" {
		return 0, false
	}
	entry := f.promptsByApproval[approvalID]
	if entry == nil {
		return 0, false
	}
	if entry.PromptEventID != "" {
		delete(f.promptsByEventID, entry.PromptEventID)
	}
	entry.PromptVersion++
	entry.PromptEventID = eventID
	entry.PromptMessageID = messageID
	f.promptsByEventID[eventID] = approvalID
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

// dropPromptLocked removes a prompt registration.
// Must be called with f.mu held.
func (f *ApprovalFlow[D]) dropPromptLocked(approvalID string) {
	approvalID = strings.TrimSpace(approvalID)
	if approvalID == "" {
		return
	}
	entry := f.promptsByApproval[approvalID]
	if entry != nil && entry.PromptEventID != "" {
		delete(f.promptsByEventID, entry.PromptEventID)
	}
	delete(f.promptsByApproval, approvalID)
}

// matchReaction checks whether a reaction targets a known approval prompt.
func (f *ApprovalFlow[D]) matchReaction(targetEventID id.EventID, sender id.UserID, key string, now time.Time) ApprovalPromptReactionMatch {
	targetEventID = id.EventID(strings.TrimSpace(targetEventID.String()))
	key = normalizeReactionKey(key)
	if targetEventID == "" || key == "" {
		return ApprovalPromptReactionMatch{}
	}

	f.mu.Lock()
	approvalID := f.promptsByEventID[targetEventID]
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
				ApprovalID: promptCopy.ApprovalID,
				Approved:   opt.Approved,
				Always:     opt.Always,
				Reason:     opt.decisionReason(),
			}
			return match
		}
	}
	match.RejectReason = RejectReasonInvalidOption
	return match
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
// store, sends it via the configured sender, binds the event ID, and queues
// prefill reactions.
func (f *ApprovalFlow[D]) SendPrompt(ctx context.Context, portal *bridgev2.Portal, params SendPromptParams) {
	if f == nil || portal == nil || portal.MXID == "" {
		return
	}
	login := f.login()
	if login == nil {
		return
	}
	approvalID := strings.TrimSpace(params.ApprovalID)

	prompt := BuildApprovalPromptMessage(params.ApprovalPromptMessageParams)
	sender := f.senderOrEmpty(portal)

	f.mu.Lock()
	prevPrompt, hadPrevPrompt := f.promptsByApproval[approvalID], false
	var prevPromptCopy ApprovalPromptRegistration
	if prevPrompt != nil {
		prevPromptCopy = *prevPrompt
		hadPrevPrompt = true
	}
	f.registerPromptLocked(ApprovalPromptRegistration{
		ApprovalID:     approvalID,
		RoomID:         params.RoomID,
		OwnerMXID:      params.OwnerMXID,
		ToolCallID:     strings.TrimSpace(params.ToolCallID),
		ToolName:       strings.TrimSpace(params.ToolName),
		TurnID:         strings.TrimSpace(params.TurnID),
		Presentation:   prompt.Presentation,
		ExpiresAt:      params.ExpiresAt,
		Options:        prompt.Options,
		PromptSenderID: sender.Sender,
	})
	f.mu.Unlock()

	var dbMeta any
	if f.dbMetadata != nil {
		dbMeta = f.dbMetadata(prompt)
	} else {
		dbMeta = &BaseMessageMetadata{
			Role:               "assistant",
			CanonicalSchema:    "ai-sdk-ui-message-v1",
			CanonicalUIMessage: prompt.UIMessage,
			ExcludeFromHistory: true,
		}
	}

	converted := &bridgev2.ConvertedMessage{
		Parts: []*bridgev2.ConvertedMessagePart{{
			ID:         networkid.PartID("0"),
			Type:       event.EventMessage,
			Content:    &event.MessageEventContent{MsgType: event.MsgNotice, Body: prompt.Body},
			Extra:      prompt.Raw,
			DBMetadata: dbMeta,
		}},
	}

	eventID, msgID, err := f.send(ctx, portal, converted)
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
	promptVersion, bound := f.bindPromptIDsLocked(approvalID, eventID, msgID)
	f.mu.Unlock()
	if !bound {
		return
	}

	f.sendPrefillReactions(ctx, portal, login, msgID, prompt.Options)
	f.schedulePromptTimeout(approvalID, params.ExpiresAt, promptVersion)
}

// ---------------------------------------------------------------------------
// Reaction handling (satisfies ApprovalReactionHandler)
// ---------------------------------------------------------------------------

// HandleReaction checks whether a reaction targets a known approval prompt.
// If so, it validates room, resolves the approval (via channel or DeliverDecision),
// and redacts prompt reactions.
func (f *ApprovalFlow[D]) HandleReaction(ctx context.Context, msg *bridgev2.MatrixReaction, targetEventID id.EventID, emoji string) bool {
	if f == nil || msg == nil || msg.Event == nil || msg.Portal == nil {
		return false
	}
	match := f.matchReaction(targetEventID, msg.Event.Sender, emoji, time.Now())
	if !match.KnownPrompt {
		return false
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
		if p != nil {
			select {
			case p.ch <- match.Decision:
				resolved = true
			default:
				if f.sendNotice != nil {
					f.sendNotice(ctx, msg.Portal, ApprovalErrorToastText(ErrApprovalAlreadyHandled))
				}
			}
		}
	}

	if resolved {
		f.FinishResolved(approvalID, match.Decision)
	}
	return true
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

func (f *ApprovalFlow[D]) redactSingleReaction(msg *bridgev2.MatrixReaction) {
	if f.testRedactSingleReaction != nil {
		f.testRedactSingleReaction(msg)
		return
	}
	login := f.login()
	sender := f.senderOrEmpty(msg.Portal)
	triggerID := msg.Event.ID
	portal := msg.Portal
	go func() {
		ctx := context.Background()
		if f.backgroundCtx != nil {
			ctx = f.backgroundCtx(ctx)
		}
		_ = RedactEventAsSender(ctx, login, portal, sender, triggerID)
	}()
}

func (f *ApprovalFlow[D]) senderOrEmpty(portal *bridgev2.Portal) bridgev2.EventSender {
	if f.sender != nil {
		return f.sender(portal)
	}
	return bridgev2.EventSender{}
}

func (f *ApprovalFlow[D]) send(_ context.Context, portal *bridgev2.Portal, converted *bridgev2.ConvertedMessage) (id.EventID, networkid.MessageID, error) {
	login := f.login()
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

func (f *ApprovalFlow[D]) sendPrefillReactions(_ context.Context, portal *bridgev2.Portal, login *bridgev2.UserLogin, msgID networkid.MessageID, options []ApprovalOption) {
	if login == nil || portal == nil || msgID == "" {
		return
	}
	sender := f.senderOrEmpty(portal)
	now := time.Now()
	seenKeys := map[string]struct{}{}
	for _, option := range options {
		for _, key := range option.allKeys() {
			if key == "" {
				continue
			}
			if _, exists := seenKeys[key]; exists {
				continue
			}
			seenKeys[key] = struct{}{}
			login.QueueRemoteEvent(&RemoteReaction{
				Portal:        portal.PortalKey,
				Sender:        sender,
				TargetMessage: msgID,
				Emoji:         key,
				EmojiID:       networkid.EmojiID(key),
				Timestamp:     now,
				LogKey:        f.logKey,
			})
		}
	}
}

func (f *ApprovalFlow[D]) schedulePromptTimeout(approvalID string, expiresAt time.Time, _ uint64) {
	approvalID = strings.TrimSpace(approvalID)
	if approvalID == "" || expiresAt.IsZero() {
		return
	}
	if time.Until(expiresAt) <= 0 {
		f.finishTimedOutApproval(approvalID, 0)
		return
	}
	// Wake the reaper so it picks up the new expiry promptly.
	select {
	case f.reaperNotify <- struct{}{}:
	default:
	}
}

func (f *ApprovalFlow[D]) finishTimedOutApproval(approvalID string, promptVersion uint64) {
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
		select {
		case <-p.done:
		default:
			close(p.done)
		}
	}
}

func approvalOptionKeyForDecision(options []ApprovalOption, decision ApprovalDecisionPayload) string {
	options = normalizeApprovalOptions(options, DefaultApprovalOptions())
	if decision.Approved {
		if decision.Always {
			for _, option := range options {
				if option.Approved && option.Always {
					return option.Key
				}
			}
		}
		for _, option := range options {
			if option.Approved && !option.Always {
				return option.Key
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
			return option.Key
		}
	}
	return ""
}

func (f *ApprovalFlow[D]) mirrorRemoteDecisionReaction(ctx context.Context, prompt ApprovalPromptRegistration, decision ApprovalDecisionPayload) {
	reactionKey := approvalOptionKeyForDecision(prompt.Options, decision)
	if reactionKey == "" {
		return
	}
	login := f.login()
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
	targetMessage := prompt.PromptMessageID
	if targetMessage == "" {
		receiver := portal.Receiver
		if receiver == "" {
			receiver = login.ID
		}
		target := resolveApprovalPromptMessage(ctx, login, receiver, prompt)
		if target == nil {
			return
		}
		targetMessage = target.ID
	}
	login.QueueRemoteEvent(&RemoteReaction{
		Portal:        portal.PortalKey,
		Sender:        sender,
		TargetMessage: targetMessage,
		Emoji:         reactionKey,
		EmojiID:       networkid.EmojiID(reactionKey),
		Timestamp:     time.Now(),
		LogKey:        f.logKey,
	})
}

func (f *ApprovalFlow[D]) finalize(approvalID string, decision *ApprovalDecisionPayload, resolved bool) {
	f.finalizeWithPromptVersion(approvalID, decision, resolved, 0)
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
		select {
		case <-p.done:
		default:
			close(p.done)
		}
	}
	delete(f.pending, approvalID)
	if entry := f.promptsByApproval[approvalID]; entry != nil {
		copyEntry := *entry
		prompt = &copyEntry
	}
	f.dropPromptLocked(approvalID)
	f.mu.Unlock()
	if prompt == nil {
		return true
	}
	login := f.login()
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
		if resolved && decision != nil {
			if f.testEditPromptToResolvedState != nil {
				f.testEditPromptToResolvedState(ctx, login, portal, sender, prompt, *decision)
			} else {
				f.editPromptToResolvedState(ac, prompt, *decision)
			}
		}
		if f.testRedactPromptPlaceholderReacts != nil {
			_ = f.testRedactPromptPlaceholderReacts(ctx, login, portal, sender, prompt)
			return
		}
		_ = RedactApprovalPromptPlaceholderReactions(ac.ctx, ac.login, ac.portal, ac.sender, prompt)
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
	if ac.login == nil || ac.portal == nil || ac.portal.MXID == "" || prompt.PromptMessageID == "" {
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
	topLevelExtra := map[string]any{}
	for key, value := range response.Raw {
		switch key {
		case "msgtype", "body", "m.relates_to":
			continue
		default:
			topLevelExtra[key] = value
		}
	}
	edit := turns.BuildConvertedEdit(&event.MessageEventContent{
		MsgType: event.MsgNotice,
		Body:    response.Body,
	}, topLevelExtra)
	if edit == nil {
		return
	}
	ac.login.QueueRemoteEvent(&RemoteEdit{
		Portal:        ac.portal.PortalKey,
		Sender:        ac.sender,
		TargetMessage: prompt.PromptMessageID,
		Timestamp:     time.Now(),
		PreBuilt:      edit,
		LogKey:        f.logKey,
	})
}
