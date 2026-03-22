package agentremote

import (
	"context"
	"encoding/json"
	"strings"

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

// EnsureSyntheticReactionSenderGhost ensures the backing ghost row exists for
// the synthetic Matrix-side sender namespace (mxid:<user>) used for local
// Matrix reaction pre-handling.
func EnsureSyntheticReactionSenderGhost(ctx context.Context, login *bridgev2.UserLogin, userID id.UserID) error {
	if login == nil || login.Bridge == nil || login.Bridge.DB == nil || login.Bridge.DB.Ghost == nil {
		return nil
	}
	senderID := MatrixSenderID(userID)
	if senderID == "" {
		return nil
	}
	existing, err := login.Bridge.DB.Ghost.GetByID(ctx, senderID)
	if err != nil {
		return err
	}
	if existing != nil {
		return nil
	}
	if err = login.Bridge.DB.Ghost.Insert(ctx, &database.Ghost{
		ID: senderID,
	}); err == nil {
		return nil
	}
	// Another concurrent handler may have inserted the row first.
	existing, lookupErr := login.Bridge.DB.Ghost.GetByID(ctx, senderID)
	if lookupErr == nil && existing != nil {
		return nil
	}
	return err
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
	var rc ReactionContext
	if msg != nil && msg.PreHandleResp != nil {
		rc.Emoji = msg.PreHandleResp.Emoji
	}
	if rc.Emoji == "" && content != nil {
		rc.Emoji = normalizeReactionKey(content.RelatesTo.Key)
	}
	if msg != nil && msg.TargetMessage != nil && msg.TargetMessage.MXID != "" {
		rc.TargetEventID = msg.TargetMessage.MXID
	} else if content != nil && content.RelatesTo.EventID != "" {
		rc.TargetEventID = content.RelatesTo.EventID
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
	receiver networkid.UserLoginID,
	prompt ApprovalPromptRegistration,
) *database.Message {
	if login == nil || login.Bridge == nil {
		return nil
	}
	msgDB := login.Bridge.DB.Message
	if prompt.PromptMessageID != "" {
		if msg, err := msgDB.GetFirstPartByID(ctx, receiver, prompt.PromptMessageID); err == nil && msg != nil {
			return msg
		}
	}
	if prompt.PromptEventID != "" {
		if msg, err := msgDB.GetPartByMXID(ctx, prompt.PromptEventID); err == nil && msg != nil {
			return msg
		}
	}
	return nil
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
	receiver := portal.Receiver
	if receiver == "" {
		receiver = login.ID
	}
	if receiver == "" {
		return nil
	}
	targetMessage := resolveApprovalPromptMessage(ctx, login, receiver, prompt)
	if targetMessage == nil {
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
