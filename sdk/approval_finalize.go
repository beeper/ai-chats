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
