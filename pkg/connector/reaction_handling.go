package connector

import (
	"context"
	"time"

	"go.mau.fi/util/variationselector"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"
)

func toolApprovalDecisionFromEmoji(emoji string) (approve bool, always bool, ok bool) {
	switch emoji {
	case "👍":
		return true, false, true
	case "⭐":
		return true, true, true
	case "👎":
		return false, false, true
	default:
		return false, false, false
	}
}

func (oc *AIClient) PreHandleMatrixReaction(ctx context.Context, msg *bridgev2.MatrixReaction) (bridgev2.MatrixReactionPreResponse, error) {
	if msg == nil || msg.Event == nil || msg.Content == nil {
		return bridgev2.MatrixReactionPreResponse{}, bridgev2.ErrReactionsNotSupported
	}
	if msg.Portal != nil {
		meta := portalMeta(msg.Portal)
		if meta != nil && meta.IsOpenCodeRoom {
			return bridgev2.MatrixReactionPreResponse{}, bridgev2.ErrReactionsNotSupported
		}
	}

	emoji := variationselector.Remove(msg.Content.RelatesTo.Key)
	return bridgev2.MatrixReactionPreResponse{
		SenderID:     oc.matrixSenderID(msg.Event.Sender),
		Emoji:        emoji,
		MaxReactions: 1,
	}, nil
}

func (oc *AIClient) HandleMatrixReaction(ctx context.Context, msg *bridgev2.MatrixReaction) (*database.Reaction, error) {
	if msg == nil || msg.Event == nil || msg.Portal == nil || msg.Content == nil {
		return &database.Reaction{}, nil
	}
	if oc.isMatrixBotUser(ctx, msg.Event.Sender) {
		return &database.Reaction{}, nil
	}

	emoji := ""
	if msg.PreHandleResp != nil {
		emoji = msg.PreHandleResp.Emoji
	}
	if emoji == "" && msg.Content != nil {
		emoji = variationselector.Remove(msg.Content.RelatesTo.Key)
	}

	targetEventID := id.EventID("")
	if msg.TargetMessage != nil && msg.TargetMessage.MXID != "" {
		targetEventID = msg.TargetMessage.MXID
	} else if msg.Content != nil && msg.Content.RelatesTo.EventID != "" {
		targetEventID = msg.Content.RelatesTo.EventID
	}

	// Owner-only tool approvals via reactions on tool-call timeline messages.
	// If the reaction matches a pending approval, resolve and do not enqueue as feedback.
	if oc != nil && oc.UserLogin != nil && msg.Event.Sender == oc.UserLogin.UserMXID {
		if approve, always, ok := toolApprovalDecisionFromEmoji(emoji); ok && targetEventID != "" {
			err := oc.resolveToolApprovalByTargetEvent(msg.Portal.MXID, targetEventID, ToolApprovalDecision{
				Approve:   approve,
				Always:    always,
				DecidedAt: time.Now(),
				DecidedBy: msg.Event.Sender,
			})
			if err == nil {
				return &database.Reaction{}, nil
			}
		}
	}

	messageID := ""
	if msg.TargetMessage != nil && msg.TargetMessage.MXID != "" {
		messageID = msg.TargetMessage.MXID.String()
	} else if targetEventID != "" {
		messageID = targetEventID.String()
	}

	feedback := ReactionFeedback{
		Emoji:     emoji,
		Timestamp: time.UnixMilli(msg.Event.Timestamp),
		Sender:    oc.matrixDisplayName(ctx, msg.Portal.MXID, msg.Event.Sender),
		MessageID: messageID,
		RoomName:  portalRoomName(msg.Portal),
		Action:    "added",
	}
	EnqueueReactionFeedback(msg.Portal.MXID, feedback)

	return &database.Reaction{}, nil
}

func (oc *AIClient) HandleMatrixReactionRemove(ctx context.Context, msg *bridgev2.MatrixReactionRemove) error {
	if msg == nil || msg.Event == nil || msg.Portal == nil || msg.TargetReaction == nil {
		return nil
	}
	if oc.isMatrixBotUser(ctx, msg.Event.Sender) {
		return nil
	}

	if err := oc.UserLogin.Bridge.DB.Reaction.Delete(ctx, msg.TargetReaction); err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to delete reaction from database")
	}

	emoji := msg.TargetReaction.Emoji
	if emoji == "" {
		emoji = string(msg.TargetReaction.EmojiID)
	}
	emoji = variationselector.Remove(emoji)

	messageID := ""
	receiver := msg.Portal.Receiver
	if receiver == "" && oc.UserLogin != nil {
		receiver = oc.UserLogin.ID
	}
	if receiver != "" {
		if targetPart, err := oc.UserLogin.Bridge.DB.Message.GetPartByID(ctx, receiver, msg.TargetReaction.MessageID, msg.TargetReaction.MessagePartID); err == nil && targetPart != nil {
			messageID = targetPart.MXID.String()
		}
	}
	if messageID == "" {
		messageID = string(msg.TargetReaction.MessageID)
	}

	feedback := ReactionFeedback{
		Emoji:     emoji,
		Timestamp: time.UnixMilli(msg.Event.Timestamp),
		Sender:    oc.matrixDisplayName(ctx, msg.Portal.MXID, msg.Event.Sender),
		MessageID: messageID,
		RoomName:  portalRoomName(msg.Portal),
		Action:    "removed",
	}
	EnqueueReactionFeedback(msg.Portal.MXID, feedback)

	return nil
}

func (oc *AIClient) matrixSenderID(userID id.UserID) networkid.UserID {
	if userID == "" {
		return ""
	}
	return networkid.UserID("mxid:" + userID.String())
}

func (oc *AIClient) matrixDisplayName(ctx context.Context, roomID id.RoomID, userID id.UserID) string {
	if userID == "" || oc == nil || oc.UserLogin == nil || oc.UserLogin.Bridge == nil || oc.UserLogin.Bridge.Matrix == nil {
		return userID.Localpart()
	}
	member, err := oc.UserLogin.Bridge.Matrix.GetMemberInfo(ctx, roomID, userID)
	if err == nil && member != nil && member.Displayname != "" {
		return member.Displayname
	}
	return userID.Localpart()
}

func (oc *AIClient) isMatrixBotUser(ctx context.Context, userID id.UserID) bool {
	if userID == "" || oc == nil || oc.UserLogin == nil || oc.UserLogin.Bridge == nil {
		return false
	}
	if oc.UserLogin.Bridge.Bot != nil && oc.UserLogin.Bridge.Bot.GetMXID() == userID {
		return true
	}
	ghost, err := oc.UserLogin.Bridge.GetGhostByMXID(ctx, userID)
	return err == nil && ghost != nil
}

func portalRoomName(portal *bridgev2.Portal) string {
	if portal == nil {
		return ""
	}
	meta := portalMeta(portal)
	if meta == nil {
		return ""
	}
	if meta.Title != "" {
		return meta.Title
	}
	if meta.Slug != "" {
		return meta.Slug
	}
	return ""
}
