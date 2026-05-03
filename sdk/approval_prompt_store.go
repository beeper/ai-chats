package sdk

import (
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2/networkid"
)

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
