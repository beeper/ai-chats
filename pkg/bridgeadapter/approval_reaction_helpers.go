package bridgeadapter

import (
	"context"
	"encoding/json"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// MatrixSenderID returns the standard networkid.UserID for a Matrix user.
func MatrixSenderID(userID id.UserID) networkid.UserID {
	if userID == "" {
		return ""
	}
	return networkid.UserID("mxid:" + userID.String())
}

// EnsureReactionContent lazily parses the reaction content from a MatrixReaction.
func EnsureReactionContent(msg *bridgev2.MatrixReaction) *event.ReactionEventContent {
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

// PreHandleApprovalReaction implements the common PreHandleMatrixReaction logic
// shared by all bridges. The SenderID is derived from the Matrix sender.
func PreHandleApprovalReaction(msg *bridgev2.MatrixReaction) (bridgev2.MatrixReactionPreResponse, error) {
	if msg == nil || msg.Event == nil {
		return bridgev2.MatrixReactionPreResponse{}, bridgev2.ErrReactionsNotSupported
	}
	content := EnsureReactionContent(msg)
	if content == nil {
		return bridgev2.MatrixReactionPreResponse{}, bridgev2.ErrReactionsNotSupported
	}
	return bridgev2.MatrixReactionPreResponse{
		SenderID:     MatrixSenderID(msg.Event.Sender),
		Emoji:        normalizeReactionKey(content.RelatesTo.Key),
		MaxReactions: 1,
	}, nil
}

// ReactionContext holds the extracted emoji and target event ID from a reaction.
type ReactionContext struct {
	Emoji         string
	TargetEventID id.EventID
}

// ExtractReactionContext pulls the emoji and target event ID from a MatrixReaction.
func ExtractReactionContext(msg *bridgev2.MatrixReaction) ReactionContext {
	content := EnsureReactionContent(msg)
	emoji := ""
	if msg != nil && msg.PreHandleResp != nil {
		emoji = msg.PreHandleResp.Emoji
	}
	if emoji == "" && content != nil {
		emoji = normalizeReactionKey(content.RelatesTo.Key)
	}
	targetEventID := id.EventID("")
	if msg != nil && msg.TargetMessage != nil && msg.TargetMessage.MXID != "" {
		targetEventID = msg.TargetMessage.MXID
	} else if content != nil && content.RelatesTo.EventID != "" {
		targetEventID = content.RelatesTo.EventID
	}
	return ReactionContext{Emoji: emoji, TargetEventID: targetEventID}
}

// RedactApprovalPromptReactions redacts all reactions on targetMessage except keepEventID.
// If targetMessage is nil and keepEventID is empty, triggerEventID is redacted directly.
func RedactApprovalPromptReactions(
	ctx context.Context,
	login *bridgev2.UserLogin,
	portal *bridgev2.Portal,
	sender bridgev2.EventSender,
	targetMessage *database.Message,
	triggerEventID id.EventID,
	keepEventID id.EventID,
) error {
	if login == nil || portal == nil || portal.MXID == "" {
		return nil
	}
	if targetMessage == nil {
		if keepEventID == "" && triggerEventID != "" {
			return RedactEventAsSender(ctx, login, portal, sender, triggerEventID)
		}
		return nil
	}
	receiver := portal.Receiver
	if receiver == "" {
		receiver = login.ID
	}
	if receiver == "" {
		return nil
	}
	reactions, err := login.Bridge.DB.Reaction.GetAllToMessagePart(ctx, receiver, targetMessage.ID, targetMessage.PartID)
	if err != nil {
		return err
	}
	var firstErr error
	seenCurrent := false
	for _, reaction := range reactions {
		if reaction == nil || reaction.MXID == "" {
			continue
		}
		if reaction.MXID == triggerEventID {
			seenCurrent = true
		}
		if keepEventID != "" && reaction.MXID == keepEventID {
			continue
		}
		if redactErr := RedactEventAsSender(ctx, login, portal, sender, reaction.MXID); redactErr != nil && firstErr == nil {
			firstErr = redactErr
		}
	}
	if !seenCurrent && keepEventID == "" && triggerEventID != "" {
		if redactErr := RedactEventAsSender(ctx, login, portal, sender, triggerEventID); redactErr != nil && firstErr == nil {
			firstErr = redactErr
		}
	}
	return firstErr
}
