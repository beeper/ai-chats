package connector

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"maunium.net/go/mautrix/id"
)

// executeMessageRead handles the read action - sends a read receipt.
func executeMessageRead(ctx context.Context, args map[string]any, btc *BridgeToolContext) (string, error) {
	// Get target message ID (optional - defaults to triggering message)
	var targetEventID id.EventID
	if msgID, ok := args["message_id"].(string); ok && msgID != "" {
		targetEventID = id.EventID(msgID)
	} else if btc.SourceEventID != "" {
		targetEventID = btc.SourceEventID
	}

	if targetEventID == "" {
		return "", errors.New("action=read requires 'message_id' parameter (no triggering message available)")
	}

	err := sendMatrixReadReceipt(ctx, btc, targetEventID)
	if err != nil {
		return "", fmt.Errorf("failed to send read receipt: %w", err)
	}

	return jsonActionResult("read", map[string]any{
		"message_id": targetEventID,
		"status":     "sent",
	})
}

// executeMessageChannelInfo handles the channel-info action - gets room information.
func executeMessageChannelInfo(ctx context.Context, _ map[string]any, btc *BridgeToolContext) (string, error) {
	info, err := getMatrixRoomInfo(ctx, btc)
	if err != nil {
		return "", fmt.Errorf("failed to get room info: %w", err)
	}

	if info == nil {
		return "", errors.New("room info not available")
	}

	return jsonActionResult("channel-info", map[string]any{
		"room_id":      info.RoomID,
		"name":         info.Name,
		"topic":        info.Topic,
		"member_count": info.MemberCount,
	})
}

// executeMessageChannelEdit handles channel-edit by mapping to room title/topic updates.
func executeMessageChannelEdit(ctx context.Context, args map[string]any, btc *BridgeToolContext) (string, error) {
	var title string
	if raw, ok := args["name"]; ok {
		if s, ok := raw.(string); ok {
			title = strings.TrimSpace(s)
		} else {
			return "", errors.New("action=channel-edit requires 'name' to be a string")
		}
	}

	descProvided := false
	description := ""
	if raw, ok := args["topic"]; ok {
		descProvided = true
		if s, ok := raw.(string); ok {
			description = strings.TrimSpace(s)
		} else {
			return "", errors.New("action=channel-edit requires 'topic' to be a string")
		}
	}

	if title == "" && !descProvided {
		return "", errors.New("action=channel-edit requires 'name' or 'topic'")
	}

	if btc == nil {
		btc = GetBridgeToolContext(ctx)
	}
	if btc == nil {
		return "", errors.New("bridge context not available")
	}
	if btc.Portal == nil {
		return "", errors.New("portal not available")
	}

	updates := make([]string, 0, 2)
	if title != "" {
		if err := btc.Client.setRoomName(ctx, btc.Portal, title); err != nil {
			return "", fmt.Errorf("failed to set room title: %w", err)
		}
		updates = append(updates, fmt.Sprintf("title=%s", title))
	}
	if descProvided {
		if err := btc.Client.setRoomTopic(ctx, btc.Portal, description); err != nil {
			return "", fmt.Errorf("failed to set room description: %w", err)
		}
		if description == "" {
			updates = append(updates, "description=cleared")
		} else {
			updates = append(updates, fmt.Sprintf("description=%s", description))
		}
	}

	result := map[string]any{
		"status":  "updated",
		"updates": updates,
	}
	if title != "" {
		result["name"] = title
	}
	if descProvided {
		result["topic"] = description
	}

	return jsonActionResult("channel-edit", result)
}

// executeMessageMemberInfo handles the member-info action - gets user profile.
func executeMessageMemberInfo(ctx context.Context, args map[string]any, btc *BridgeToolContext) (string, error) {
	userIDStr, ok := args["user_id"].(string)
	if !ok || userIDStr == "" {
		return "", errors.New("action=member-info requires 'user_id' parameter")
	}

	userID := id.UserID(userIDStr)
	profile, err := getMatrixUserProfile(ctx, btc, userID)
	if err != nil {
		return "", fmt.Errorf("failed to get user profile: %w", err)
	}

	if profile == nil {
		return "", errors.New("user profile not available")
	}

	result := map[string]any{
		"user_id":      profile.UserID,
		"display_name": profile.DisplayName,
		"avatar_url":   profile.AvatarURL,
	}
	if agentID, ok := parseAgentFromGhostID(string(userID)); ok {
		var modelID string
		if btc != nil && btc.Client != nil {
			if btc.Meta != nil {
				modelID = btc.Client.effectiveModel(btc.Meta)
			} else {
				store := NewAgentStoreAdapter(btc.Client)
				if agent, err := store.GetAgentByID(ctx, agentID); err == nil && agent != nil && agent.Model.Primary != "" {
					if override := btc.Client.agentModelOverride(agentID); override != "" {
						modelID = ResolveAlias(override)
					} else {
						modelID = ResolveAlias(agent.Model.Primary)
					}
				}
			}
		}
		if modelID != "" {
			result["com.beeper.ai.model_id"] = modelID
		}
	} else if modelID := parseModelFromGhostID(string(userID)); modelID != "" {
		result["com.beeper.ai.model_id"] = modelID
	}

	return jsonActionResult("member-info", result)
}

// executeMessageReactions handles the reactions action - lists reactions on a message.
func executeMessageReactions(ctx context.Context, args map[string]any, btc *BridgeToolContext) (string, error) {
	// Get target message ID (required for listing reactions)
	msgID, ok := args["message_id"].(string)
	if !ok || msgID == "" {
		return "", errors.New("action=reactions requires 'message_id' parameter")
	}
	targetEventID := id.EventID(msgID)

	reactions, err := listMatrixReactions(ctx, btc, targetEventID)
	if err != nil {
		return "", fmt.Errorf("failed to list reactions: %w", err)
	}

	return jsonActionResult("reactions", map[string]any{
		"message_id": msgID,
		"reactions":  reactions,
		"count":      len(reactions),
	})
}

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

	// Get emoji to remove (empty means all)
	emoji, _ := args["emoji"].(string)

	removed, err := removeMatrixReactions(ctx, btc, targetEventID, emoji)
	if err != nil {
		return "", fmt.Errorf("failed to remove reactions: %w", err)
	}

	return jsonActionResult("react", map[string]any{
		"emoji":      emoji,
		"message_id": targetEventID,
		"removed":    removed,
		"status":     "removed",
	})
}

// executeMessageFocus handles the focus action - focuses the desktop app and optionally a chat/message.
func executeMessageFocus(ctx context.Context, args map[string]any, btc *BridgeToolContext) (string, error) {
	if btc == nil || btc.Client == nil {
		return "", errors.New("bridge context not available")
	}

	sessionKey := firstNonEmptyString(args["sessionKey"])
	label := firstNonEmptyString(args["label"])
	chatID := firstNonEmptyString(args["chatId"])
	instance := firstNonEmptyString(args["instance"])
	messageID := firstNonEmptyString(args["message_id"])
	draftText := firstNonEmptyString(args["draftText"], args["message"])
	draftAttachmentPath := firstNonEmptyString(args["draftAttachmentPath"])

	if sessionKey != "" && label != "" {
		return "", errors.New("action=focus requires only one of 'sessionKey' or 'label'")
	}
	if chatID != "" && (sessionKey != "" || label != "") {
		return "", errors.New("action=focus requires only one of 'chatId', 'sessionKey', or 'label'")
	}

	if sessionKey != "" {
		parsedInstance, parsedChatID, ok := parseDesktopSessionKey(sessionKey)
		if !ok {
			return "", errors.New("action=focus requires a desktop-api sessionKey")
		}
		chatID = parsedChatID
		instance = parsedInstance
	} else if label != "" {
		if instance != "" {
			resolvedInstance, resolveErr := btc.Client.resolveDesktopInstanceName(instance)
			if resolveErr != nil {
				return "", resolveErr
			}
			resolvedID, resolvedKey, err := btc.Client.resolveDesktopSessionByLabel(ctx, resolvedInstance, label)
			if err != nil {
				return "", err
			}
			chatID = resolvedID
			sessionKey = resolvedKey
			instance = resolvedInstance
		} else {
			resolvedInstance, resolvedID, resolvedKey, err := btc.Client.resolveDesktopSessionByLabelAnyInstance(ctx, label)
			if err != nil {
				return "", err
			}
			chatID = resolvedID
			sessionKey = resolvedKey
			instance = resolvedInstance
		}
	}

	if messageID != "" && chatID == "" {
		return "", errors.New("action=focus requires chatId or sessionKey when message_id is set")
	}

	if draftAttachmentPath != "" {
		draftAttachmentPath = expandUserPath(draftAttachmentPath)
	}

	resolvedInstance, resolveErr := btc.Client.resolveDesktopInstanceName(instance)
	if resolveErr != nil {
		return "", resolveErr
	}
	instance = resolvedInstance
	if chatID != "" {
		// Always return the canonical desktop session key (includes resolved instance).
		sessionKey = normalizeDesktopSessionKeyWithInstance(instance, chatID)
	}

	_, err := btc.Client.focusDesktop(ctx, instance, desktopFocusParams{
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
		if config, ok := btc.Client.desktopAPIInstanceConfig(instance); ok {
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
