package sdk

import (
	"context"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"
)

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
			return true
		}

		found++
		if found > 1 {
			match = ApprovalPromptReactionMatch{}
			return false
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
		return true
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
		return false
	})
	return hasPending
}

func resolveApprovalReactionTargetMessageID(
	ctx context.Context,
	login *bridgev2.UserLogin,
	portal *bridgev2.Portal,
	replyToEventID id.EventID,
) networkid.MessageID {
	replyToEventID = id.EventID(strings.TrimSpace(replyToEventID.String()))
	if login == nil || login.Bridge == nil || replyToEventID == "" {
		return ""
	}
	msg, err := findPortalMessageByMXID(ctx, login, portal, replyToEventID)
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
	target := resolveApprovalPromptMessage(ctx, login, portal, prompt)
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
