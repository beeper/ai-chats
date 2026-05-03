package sdk

import (
	"context"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"
)

// PreHandleApprovalReaction implements the common PreHandleMatrixReaction logic
// shared by all bridges. Matrix-side reactions are handled ephemerally and are
// not persisted as synthetic ghost senders.
func PreHandleApprovalReaction(msg *bridgev2.MatrixReaction) (bridgev2.MatrixReactionPreResponse, error) {
	if msg == nil || msg.Event == nil {
		return bridgev2.MatrixReactionPreResponse{}, bridgev2.ErrReactionsNotSupported
	}
	if msg.Content == nil {
		return bridgev2.MatrixReactionPreResponse{}, bridgev2.ErrReactionsNotSupported
	}
	return bridgev2.MatrixReactionPreResponse{
		// Matrix-side reactions are handled ephemerally; do not persist a
		// synthetic ghost sender for them.
		SenderID:     "",
		Emoji:        normalizeReactionKey(msg.Content.RelatesTo.Key),
		MaxReactions: 1,
	}, nil
}

// ReactionContext holds the extracted emoji plus the target message/event IDs.
type ReactionContext struct {
	TargetMessageID networkid.MessageID
	Emoji           string
	TargetEventID   id.EventID
}

// ExtractReactionContext pulls the emoji and target identifiers from a MatrixReaction.
func ExtractReactionContext(msg *bridgev2.MatrixReaction) ReactionContext {
	var rc ReactionContext
	if msg != nil && msg.TargetMessage != nil {
		rc.TargetMessageID = msg.TargetMessage.ID
	}
	if msg != nil && msg.PreHandleResp != nil {
		rc.Emoji = msg.PreHandleResp.Emoji
	}
	if rc.Emoji == "" && msg != nil && msg.Content != nil {
		rc.Emoji = normalizeReactionKey(msg.Content.RelatesTo.Key)
	}
	if msg != nil && msg.TargetMessage != nil && msg.TargetMessage.MXID != "" {
		rc.TargetEventID = msg.TargetMessage.MXID
	} else if msg != nil && msg.Content != nil && msg.Content.RelatesTo.EventID != "" {
		rc.TargetEventID = msg.Content.RelatesTo.EventID
	}
	return rc
}

func approvalPromptPlaceholderSenderID(prompt ApprovalPromptRegistration, sender bridgev2.EventSender) networkid.UserID {
	if prompt.PromptSenderID != "" {
		return prompt.PromptSenderID
	}
	return sender.Sender
}

func isApprovalPlaceholderReaction(reaction *database.Reaction, prompt ApprovalPromptRegistration, sender bridgev2.EventSender) bool {
	if reaction == nil {
		return false
	}
	placeholderSenderID := strings.TrimSpace(string(approvalPromptPlaceholderSenderID(prompt, sender)))
	if placeholderSenderID == "" {
		return false
	}
	return strings.TrimSpace(string(reaction.SenderID)) == placeholderSenderID
}

type ApprovalPromptReactionCleanupOptions struct {
	PreserveSenderID networkid.UserID
	PreserveKey      string
}

func shouldPreserveApprovalReaction(
	reaction *database.Reaction,
	opts ApprovalPromptReactionCleanupOptions,
) bool {
	if reaction == nil {
		return false
	}
	preserveSenderID := strings.TrimSpace(string(opts.PreserveSenderID))
	preserveKey := normalizeReactionKey(opts.PreserveKey)
	if preserveSenderID == "" || preserveKey == "" {
		return false
	}
	if strings.TrimSpace(string(reaction.SenderID)) != preserveSenderID {
		return false
	}
	return normalizeReactionKey(reaction.Emoji) == preserveKey || normalizeReactionKey(string(reaction.EmojiID)) == preserveKey
}

func resolveApprovalPromptMessage(
	ctx context.Context,
	login *bridgev2.UserLogin,
	portal *bridgev2.Portal,
	prompt ApprovalPromptRegistration,
) *database.Message {
	if login == nil || login.Bridge == nil || prompt.PromptMessageID == "" {
		return nil
	}
	msg, err := findPortalMessageByID(ctx, login, portal, prompt.PromptMessageID, networkid.PartID("0"))
	if err != nil {
		return nil
	}
	return msg
}

// RedactApprovalPromptPlaceholderReactions redacts only bridge-authored placeholder
// reactions on a known approval prompt message. User reactions are preserved.
func RedactApprovalPromptPlaceholderReactions(
	ctx context.Context,
	login *bridgev2.UserLogin,
	portal *bridgev2.Portal,
	sender bridgev2.EventSender,
	prompt ApprovalPromptRegistration,
	opts ApprovalPromptReactionCleanupOptions,
) error {
	if login == nil || portal == nil || portal.MXID == "" {
		return nil
	}
	targetMessage := resolveApprovalPromptMessage(ctx, login, portal, prompt)
	if targetMessage == nil {
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
	for _, reaction := range reactions {
		if reaction == nil || reaction.MXID == "" || !isApprovalPlaceholderReaction(reaction, prompt, sender) {
			continue
		}
		if shouldPreserveApprovalReaction(reaction, opts) {
			continue
		}
		if redactErr := RedactEventAsSender(ctx, login, portal, sender, reaction.MXID); redactErr != nil && firstErr == nil {
			firstErr = redactErr
		}
	}
	return firstErr
}
