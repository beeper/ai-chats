package bridgeadapter

import (
	"context"
	"errors"
	"slices"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/id"
)

func ApprovalOptionPrefillKeys(option ApprovalOption) []string {
	return slices.Clone(option.prefillKeys())
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

func IsApprovalDecisionTerminalError(err error) bool {
	return errors.Is(err, ErrApprovalAlreadyHandled) ||
		errors.Is(err, ErrApprovalExpired) ||
		errors.Is(err, ErrApprovalUnknown) ||
		errors.Is(err, ErrApprovalWrongRoom) ||
		errors.Is(err, ErrApprovalOnlyOwner)
}
