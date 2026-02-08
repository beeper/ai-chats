package connector

import (
	"cmp"
	"context"
	"encoding/json"
	"time"

	"go.mau.fi/util/variationselector"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
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

func (oc *AIClient) PreHandleMatrixReaction(ctx context.Context, msg *bridgev2.MatrixReaction) (bridgev2.MatrixReactionPreResponse, error) {
	if msg == nil || msg.Event == nil {
		return bridgev2.MatrixReactionPreResponse{}, bridgev2.ErrReactionsNotSupported
	}
	content := ensureReactionContent(msg)
	if content == nil {
		return bridgev2.MatrixReactionPreResponse{}, bridgev2.ErrReactionsNotSupported
	}
	if msg.Portal != nil {
		meta := portalMeta(msg.Portal)
		if meta != nil && meta.IsOpenCodeRoom {
			return bridgev2.MatrixReactionPreResponse{}, bridgev2.ErrReactionsNotSupported
		}
	}

	emoji := variationselector.Remove(content.RelatesTo.Key)
	return bridgev2.MatrixReactionPreResponse{
		SenderID:     oc.matrixSenderID(msg.Event.Sender),
		Emoji:        emoji,
		MaxReactions: 1,
	}, nil
}

func (oc *AIClient) HandleMatrixReaction(ctx context.Context, msg *bridgev2.MatrixReaction) (*database.Reaction, error) {
	if msg == nil || msg.Event == nil || msg.Portal == nil {
		return &database.Reaction{}, nil
	}
	if isMatrixBotUser(ctx, oc.UserLogin.Bridge, msg.Event.Sender) {
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
	if isMatrixBotUser(ctx, oc.UserLogin.Bridge, msg.Event.Sender) {
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

func portalRoomName(portal *bridgev2.Portal) string {
	if portal == nil {
		return ""
	}
	meta := portalMeta(portal)
	if meta == nil {
		return ""
	}
	return cmp.Or(meta.Title, meta.Slug)
}
