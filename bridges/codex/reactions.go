package codex

import (
	"context"
	"encoding/json"
	"time"

	"go.mau.fi/util/variationselector"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/pkg/bridgeadapter"
)

func ensureReactionContent(msg *bridgev2.MatrixReaction) *event.ReactionEventContent {
	if msg == nil {
		return nil
	}
	if msg.Content != nil {
		return msg.Content
	}
	if msg.Event == nil || len(msg.Event.Content.VeryRaw) == 0 {
		return nil
	}
	var parsed event.ReactionEventContent
	if err := json.Unmarshal(msg.Event.Content.VeryRaw, &parsed); err != nil {
		return nil
	}
	msg.Content = &parsed
	return msg.Content
}

func (cc *CodexClient) PreHandleMatrixReaction(ctx context.Context, msg *bridgev2.MatrixReaction) (bridgev2.MatrixReactionPreResponse, error) {
	if msg == nil || msg.Event == nil {
		return bridgev2.MatrixReactionPreResponse{}, bridgev2.ErrReactionsNotSupported
	}
	content := ensureReactionContent(msg)
	if content == nil {
		return bridgev2.MatrixReactionPreResponse{}, bridgev2.ErrReactionsNotSupported
	}
	return bridgev2.MatrixReactionPreResponse{
		SenderID:     networkid.UserID("mxid:" + msg.Event.Sender.String()),
		Emoji:        variationselector.Remove(content.RelatesTo.Key),
		MaxReactions: 1,
	}, nil
}

func (cc *CodexClient) HandleMatrixReaction(ctx context.Context, msg *bridgev2.MatrixReaction) (*database.Reaction, error) {
	if cc == nil || msg == nil || msg.Event == nil || msg.Portal == nil {
		return &database.Reaction{}, nil
	}
	if bridgeadapter.IsMatrixBotUser(ctx, cc.UserLogin.Bridge, msg.Event.Sender) {
		return &database.Reaction{}, nil
	}
	content := ensureReactionContent(msg)
	emoji := ""
	if msg.PreHandleResp != nil {
		emoji = msg.PreHandleResp.Emoji
	}
	if emoji == "" && content != nil {
		emoji = variationselector.Remove(content.RelatesTo.Key)
	}
	targetEventID := id.EventID("")
	if msg.TargetMessage != nil && msg.TargetMessage.MXID != "" {
		targetEventID = msg.TargetMessage.MXID
	} else if content != nil && content.RelatesTo.EventID != "" {
		targetEventID = content.RelatesTo.EventID
	}
	if !cc.handleApprovalPromptReaction(ctx, msg, targetEventID, emoji) {
		return &database.Reaction{}, nil
	}
	return &database.Reaction{}, nil
}

func (cc *CodexClient) HandleMatrixReactionRemove(ctx context.Context, msg *bridgev2.MatrixReactionRemove) error {
	return nil
}

func (cc *CodexClient) handleApprovalPromptReaction(ctx context.Context, msg *bridgev2.MatrixReaction, targetEventID id.EventID, emoji string) bool {
	if cc == nil || cc.approvalPrompts == nil || msg == nil || msg.Event == nil || msg.Portal == nil {
		return false
	}
	match := cc.approvalPrompts.MatchReaction(targetEventID, msg.Event.Sender, emoji, time.Now())
	if !match.KnownPrompt {
		return false
	}
	keepEventID := id.EventID("")
	if match.ShouldResolve {
		err := cc.resolveToolApproval(msg.Portal.MXID, match.ApprovalID, ToolApprovalDecisionCodex{
			Approve:   match.Decision.Approved,
			Reason:    match.Decision.Reason,
			DecidedAt: time.Now(),
			DecidedBy: msg.Event.Sender,
		})
		if err != nil {
			cc.sendSystemNotice(ctx, msg.Portal, bridgeadapter.ApprovalErrorToastText(err))
		} else {
			keepEventID = msg.Event.ID
		}
	}
	_ = bridgeadapter.RedactApprovalPromptReactions(
		ctx,
		cc.UserLogin,
		msg.Portal,
		cc.senderForPortal(),
		msg.TargetMessage,
		msg.Event.ID,
		keepEventID,
	)
	return true
}
