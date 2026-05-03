package connector

import (
	"context"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/ai-chats/pkg/shared/aihelpers"
)

func (oc *AIClient) PreHandleMatrixReaction(_ context.Context, msg *bridgev2.MatrixReaction) (bridgev2.MatrixReactionPreResponse, error) {
	return bridgev2.MatrixReactionPreResponse{}, nil
}

func (oc *AIClient) HandleMatrixReaction(ctx context.Context, msg *bridgev2.MatrixReaction) (*database.Reaction, error) {
	if oc == nil || oc.UserLogin == nil || oc.UserLogin.Bridge == nil || msg == nil || msg.Event == nil || msg.Portal == nil {
		return &database.Reaction{}, nil
	}
	if aihelpers.IsMatrixBotUser(ctx, oc.UserLogin.Bridge, msg.Event.Sender) {
		return &database.Reaction{}, nil
	}

	return &database.Reaction{}, nil
}

func (oc *AIClient) HandleMatrixReactionRemove(ctx context.Context, msg *bridgev2.MatrixReactionRemove) error {
	if oc == nil || oc.UserLogin == nil || oc.UserLogin.Bridge == nil || msg == nil || msg.Event == nil || msg.Portal == nil || msg.TargetReaction == nil {
		return nil
	}
	if aihelpers.IsMatrixBotUser(ctx, oc.UserLogin.Bridge, msg.Event.Sender) {
		return nil
	}
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
