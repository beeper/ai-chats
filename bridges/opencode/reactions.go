package opencode

import (
	"context"
	"encoding/json"

	"go.mau.fi/util/variationselector"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/pkg/bridgeadapter"
)

func ensureOpenCodeReactionContent(msg *bridgev2.MatrixReaction) *event.ReactionEventContent {
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

func (oc *OpenCodeClient) PreHandleMatrixReaction(ctx context.Context, msg *bridgev2.MatrixReaction) (bridgev2.MatrixReactionPreResponse, error) {
	if msg == nil || msg.Event == nil {
		return bridgev2.MatrixReactionPreResponse{}, bridgev2.ErrReactionsNotSupported
	}
	content := ensureOpenCodeReactionContent(msg)
	if content == nil {
		return bridgev2.MatrixReactionPreResponse{}, bridgev2.ErrReactionsNotSupported
	}
	return bridgev2.MatrixReactionPreResponse{
		SenderID:     networkid.UserID("mxid:" + msg.Event.Sender.String()),
		Emoji:        variationselector.Remove(content.RelatesTo.Key),
		MaxReactions: 1,
	}, nil
}

func (oc *OpenCodeClient) HandleMatrixReaction(ctx context.Context, msg *bridgev2.MatrixReaction) (*database.Reaction, error) {
	if oc == nil || msg == nil || msg.Event == nil || msg.Portal == nil || oc.bridge == nil {
		return &database.Reaction{}, nil
	}
	if bridgeadapter.IsMatrixBotUser(ctx, oc.UserLogin.Bridge, msg.Event.Sender) {
		return &database.Reaction{}, nil
	}
	content := ensureOpenCodeReactionContent(msg)
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
	if handled := oc.bridge.HandleApprovalPromptReaction(ctx, msg, targetEventID, emoji); handled {
		return &database.Reaction{}, nil
	}
	return &database.Reaction{}, nil
}

func (oc *OpenCodeClient) HandleMatrixReactionRemove(ctx context.Context, msg *bridgev2.MatrixReactionRemove) error {
	return nil
}
