package ai

import (
	"context"
	"strings"
	"time"

	"go.mau.fi/util/variationselector"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/sdk"
)

func (oc *AIClient) PreHandleMatrixReaction(_ context.Context, msg *bridgev2.MatrixReaction) (bridgev2.MatrixReactionPreResponse, error) {
	return sdk.PreHandleApprovalReaction(msg)
}

func (oc *AIClient) HandleMatrixReaction(ctx context.Context, msg *bridgev2.MatrixReaction) (*database.Reaction, error) {
	if oc == nil || oc.UserLogin == nil || oc.UserLogin.Bridge == nil || msg == nil || msg.Event == nil || msg.Portal == nil {
		return &database.Reaction{}, nil
	}
	if sdk.IsMatrixBotUser(ctx, oc.UserLogin.Bridge, msg.Event.Sender) {
		return &database.Reaction{}, nil
	}

	rc := sdk.ExtractReactionContext(msg)
	if oc.approvalFlow.HandleReaction(ctx, msg) {
		return &database.Reaction{}, nil
	}

	messageID := ""
	if msg.TargetMessage != nil && msg.TargetMessage.MXID != "" {
		messageID = msg.TargetMessage.MXID.String()
	} else if rc.TargetEventID != "" {
		messageID = rc.TargetEventID.String()
	}

	feedback := ReactionFeedback{
		Emoji:     rc.Emoji,
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
	if oc == nil || oc.UserLogin == nil || oc.UserLogin.Bridge == nil || msg == nil || msg.Event == nil || msg.Portal == nil || msg.TargetReaction == nil {
		return nil
	}
	if sdk.IsMatrixBotUser(ctx, oc.UserLogin.Bridge, msg.Event.Sender) {
		return nil
	}
	if oc.approvalFlow.HandleReactionRemove(ctx, msg) {
		return nil
	}

	emoji := msg.TargetReaction.Emoji
	if emoji == "" {
		emoji = string(msg.TargetReaction.EmojiID)
	}
	emoji = variationselector.Remove(emoji)

	messageID := ""
	if targetPart, err := oc.loadPortalMessagePartByID(ctx, msg.Portal, msg.TargetReaction.MessageID, msg.TargetReaction.MessagePartID); err == nil && targetPart != nil {
		messageID = targetPart.MXID.String()
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
	if name := strings.TrimSpace(portal.Name); name != "" {
		return name
	}
	meta := portalMeta(portal)
	if meta == nil {
		return ""
	}
	return strings.TrimSpace(meta.Slug)
}
