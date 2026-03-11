package bridgeadapter

import (
	"context"
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
}

// NewApprovalFlow creates an ApprovalFlow from the given config.
func NewApprovalFlow[D any](cfg ApprovalFlowConfig[D]) *ApprovalFlow[D] {
	timeout := cfg.SendTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &ApprovalFlow[D]{
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
	approvalID = strings.TrimSpace(approvalID)
	if approvalID == "" {
		return
	}
	f.mu.Lock()
	delete(f.pending, approvalID)
	f.dropPromptLocked(approvalID)
	f.mu.Unlock()
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
		f.Drop(approvalID)
		return ErrApprovalExpired
	}
	select {
	case p.ch <- decision:
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
		f.Drop(approvalID)
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
	if len(reg.Options) == 0 {
		reg.Options = normalizeApprovalOptions(reg.Options)
	}

	if prev := f.promptsByApproval[reg.ApprovalID]; prev != nil && prev.PromptEventID != "" {
		delete(f.promptsByEventID, prev.PromptEventID)
	}
	copyReg := reg
	f.promptsByApproval[reg.ApprovalID] = &copyReg
	if reg.PromptEventID != "" {
		f.promptsByEventID[reg.PromptEventID] = reg.ApprovalID
	}

	// Opportunistic sweep: remove up to 10 expired entries to prevent unbounded growth.
	now := time.Now()
	swept := 0
	for aid, entry := range f.promptsByApproval {
		if swept >= 10 {
			break
		}
		if !entry.ExpiresAt.IsZero() && now.After(entry.ExpiresAt) {
			if entry.PromptEventID != "" {
				delete(f.promptsByEventID, entry.PromptEventID)
			}
			delete(f.promptsByApproval, aid)
			swept++
		}
	}
}

// bindPromptEventLocked associates an event ID with a prompt registration.
// Must be called with f.mu held.
func (f *ApprovalFlow[D]) bindPromptEventLocked(approvalID string, eventID id.EventID) bool {
	approvalID = strings.TrimSpace(approvalID)
	eventID = id.EventID(strings.TrimSpace(eventID.String()))
	if approvalID == "" || eventID == "" {
		return false
	}
	entry := f.promptsByApproval[approvalID]
	if entry == nil {
		return false
	}
	if entry.PromptEventID != "" {
		delete(f.promptsByEventID, entry.PromptEventID)
	}
	entry.PromptEventID = eventID
	f.promptsByEventID[eventID] = approvalID
	return true
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
	if targetEventID == "" || key == "" {
		return ApprovalPromptReactionMatch{}
	}
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

	prompt := BuildApprovalPromptMessage(params.ApprovalPromptMessageParams)

	f.mu.Lock()
	f.registerPromptLocked(ApprovalPromptRegistration{
		ApprovalID: strings.TrimSpace(params.ApprovalID),
		RoomID:     params.RoomID,
		OwnerMXID:  params.OwnerMXID,
		ToolCallID: strings.TrimSpace(params.ToolCallID),
		ToolName:   strings.TrimSpace(params.ToolName),
		TurnID:     strings.TrimSpace(params.TurnID),
		ExpiresAt:  params.ExpiresAt,
		Options:    prompt.Options,
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
		return
	}

	f.mu.Lock()
	f.bindPromptEventLocked(strings.TrimSpace(params.ApprovalID), eventID)
	f.mu.Unlock()

	f.sendPrefillReactions(ctx, portal, login, msgID, prompt.Options)
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

	keepEventID := id.EventID("")
	if f.deliverDecision != nil {
		// Callback-based flow (OpenCode/OpenClaw).
		if err := f.deliverDecision(ctx, msg.Portal, p, match.Decision); err != nil {
			if f.sendNotice != nil {
				f.sendNotice(ctx, msg.Portal, ApprovalErrorToastText(err))
			}
		} else {
			keepEventID = msg.Event.ID
		}
	} else {
		// Channel-based flow (Codex).
		if p != nil {
			select {
			case p.ch <- match.Decision:
				keepEventID = msg.Event.ID
			default:
				if f.sendNotice != nil {
					f.sendNotice(ctx, msg.Portal, ApprovalErrorToastText(ErrApprovalAlreadyHandled))
				}
			}
		}
	}

	// Clean up both stores.
	f.Drop(approvalID)

	// Redact prompt reactions in background.
	f.redactPromptReactions(msg, keepEventID)
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

func (f *ApprovalFlow[D]) redactPromptReactions(msg *bridgev2.MatrixReaction, keepEventID id.EventID) {
	login := f.login()
	sender := f.senderOrEmpty(msg.Portal)
	portal := msg.Portal
	target := msg.TargetMessage
	triggerID := msg.Event.ID
	go func() {
		ctx := context.Background()
		if f.backgroundCtx != nil {
			ctx = f.backgroundCtx(ctx)
		}
		_ = RedactApprovalPromptReactions(ctx, login, portal, sender, target, triggerID, keepEventID)
	}()
}

func (f *ApprovalFlow[D]) senderOrEmpty(portal *bridgev2.Portal) bridgev2.EventSender {
	if f.sender != nil {
		return f.sender(portal)
	}
	return bridgev2.EventSender{}
}

func (f *ApprovalFlow[D]) send(ctx context.Context, portal *bridgev2.Portal, converted *bridgev2.ConvertedMessage) (id.EventID, networkid.MessageID, error) {
	sendCtx := ctx
	if f.backgroundCtx != nil {
		sendCtx = f.backgroundCtx(ctx)
	}
	sendCtx, cancel := context.WithTimeout(sendCtx, f.sendTimeout)
	defer cancel()

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
		for _, key := range option.prefillKeys() {
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
