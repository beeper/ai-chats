package ai

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"maunium.net/go/mautrix/id"
)

// executeMessageReactRemove handles reaction removal - removes the bot's reactions.
func executeMessageReactRemove(ctx context.Context, args map[string]any, btc *BridgeToolContext) (string, error) {
	// Get target message ID
	var targetEventID id.EventID
	if msgID, ok := args["message_id"].(string); ok && msgID != "" {
		targetEventID = id.EventID(msgID)
	} else if btc.SourceEventID != "" {
		targetEventID = btc.SourceEventID
	}

	if targetEventID == "" {
		return "", errors.New("action=react with remove requires 'message_id' parameter")
	}

	emoji, _ := args["emoji"].(string)
	if emoji == "" {
		return "", errors.New("action=react with remove requires an explicit emoji")
	}
	if err := btc.Client.removeReaction(ctx, btc.Portal, targetEventID, emoji); err != nil {
		return "", fmt.Errorf("failed to remove reactions: %w", err)
	}

	return jsonActionResult("react", map[string]any{
		"emoji":      emoji,
		"message_id": targetEventID,
		"removed":    1,
		"status":     "removed",
	})
}

// executeMessageFocus handles the focus action - focuses the desktop app and optionally a chat/message.
func executeMessageFocus(ctx context.Context, args map[string]any, btc *BridgeToolContext) (string, error) {
	if btc == nil || btc.Client == nil {
		return "", errors.New("bridge context not available")
	}

	messageID := firstNonEmptyString(args["message_id"])
	draftText := firstNonEmptyString(args["draftText"], args["message"])
	draftAttachmentPath := firstNonEmptyString(args["draftAttachmentPath"])

	instance, chatID, sessionKey, _, err := resolveDesktopMessageTarget(ctx, btc.Client, args, false)
	if err != nil {
		return "", err
	}

	if messageID != "" && chatID == "" {
		return "", errors.New("action=focus requires chatId or sessionKey when message_id is set")
	}

	if draftAttachmentPath != "" {
		draftAttachmentPath = expandUserPath(draftAttachmentPath)
	}

	_, err = btc.Client.focusDesktop(ctx, instance, desktopFocusParams{
		ChatID:              chatID,
		MessageID:           messageID,
		DraftText:           draftText,
		DraftAttachmentPath: draftAttachmentPath,
	})
	if err != nil {
		return "", fmt.Errorf("failed to focus desktop: %w", err)
	}

	result := map[string]any{
		"status": "ok",
	}
	if chatID != "" {
		result["chatId"] = chatID
	}
	if sessionKey != "" {
		result["sessionKey"] = sessionKey
	} else if chatID != "" {
		result["sessionKey"] = normalizeDesktopSessionKeyWithInstance(instance, chatID)
	}
	if instance != "" {
		result["instance"] = instance
		if config, ok := btc.Client.desktopAPIInstanceConfig(ctx, instance); ok {
			if baseURL := strings.TrimSpace(config.BaseURL); baseURL != "" {
				result["baseUrl"] = baseURL
			}
		}
	}
	if messageID != "" {
		result["message_id"] = messageID
	}
	if draftText != "" {
		result["draftText"] = draftText
	}
	if draftAttachmentPath != "" {
		result["draftAttachmentPath"] = draftAttachmentPath
	}

	return jsonActionResult("focus", result)
}
