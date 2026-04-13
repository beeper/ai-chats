package sdk

import (
	"context"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// ---------------------------------------------------------------------------
// Prompt store (inlined)
// ---------------------------------------------------------------------------

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
	reactionTargetMessageID := resolveApprovalReactionTargetMessageID(ctx, login, portal, params.ReplyToEventID)

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
		_ = RedactEventAsSender(ctx, login, portal, sender, triggerID)
	}()
}

func (f *ApprovalFlow[D]) reactionRedactionSender(msg *bridgev2.MatrixReaction) bridgev2.EventSender {
	if msg != nil && msg.Portal != nil {
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
	sender := f.senderOrEmpty(portal)
	if f.testMirrorRemoteDecisionReaction != nil {
		f.testMirrorRemoteDecisionReaction(ctx, login, portal, sender, prompt, reactionKey)
		return
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
	timing := ResolveEventTiming(time.Now(), 0)
	ac.login.QueueRemoteEvent(&RemoteEdit{
		Portal:        ac.portal.PortalKey,
		Sender:        ac.sender,
		TargetMessage: targetMessage,
		Timestamp:     timing.Timestamp,
		StreamOrder:   timing.StreamOrder,
		PreBuilt:      edit,
		LogKey:        f.logKey,
	})
}
