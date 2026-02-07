package connector

import (
	"context"
	"errors"
	"fmt"
	"strings"

	beeperdesktopapi "github.com/beeper/desktop-api-go"
)

func hasDesktopMessageTargetHints(args map[string]any) bool {
	if args == nil {
		return false
	}
	if firstNonEmptyString(args["sessionKey"]) != "" {
		return true
	}
	if firstNonEmptyString(args["label"]) != "" {
		return true
	}
	if firstNonEmptyString(args["chatId"]) != "" {
		return true
	}
	if firstNonEmptyString(args["instance"]) != "" {
		return true
	}
	return false
}

func resolveDesktopMessageTarget(ctx context.Context, client *AIClient, args map[string]any, requireChat bool) (instance string, chatID string, sessionKey string, resolved bool, err error) {
	if client == nil || !hasDesktopMessageTargetHints(args) {
		return "", "", "", false, nil
	}

	sessionKey = firstNonEmptyString(args["sessionKey"])
	label := firstNonEmptyString(args["label"])
	chatID = firstNonEmptyString(args["chatId"])
	instance = firstNonEmptyString(args["instance"])
	resolveOpts := desktopLabelResolveOptions{
		AccountID: strings.TrimSpace(firstNonEmptyString(args["accountId"])),
		Network:   strings.TrimSpace(firstNonEmptyString(args["network"])),
	}

	if sessionKey != "" && label != "" {
		return "", "", "", true, errors.New("provide only one of 'sessionKey' or 'label'")
	}
	if chatID != "" && (sessionKey != "" || label != "") {
		return "", "", "", true, errors.New("provide only one of 'chatId', 'sessionKey', or 'label'")
	}

	if sessionKey != "" {
		parsedInstance, parsedChatID, ok := parseDesktopSessionKey(sessionKey)
		if !ok {
			return "", "", "", true, errors.New("sessionKey must be a desktop-api session")
		}
		resolvedInstance, resolveErr := client.resolveDesktopInstanceName(parsedInstance)
		if resolveErr != nil {
			return "", "", "", true, resolveErr
		}
		return resolvedInstance, parsedChatID, normalizeDesktopSessionKeyWithInstance(resolvedInstance, parsedChatID), true, nil
	}

	if label != "" {
		if instance != "" {
			resolvedInstance, resolveErr := client.resolveDesktopInstanceName(instance)
			if resolveErr != nil {
				return "", "", "", true, resolveErr
			}
			resolvedID, resolvedKey, resolveErr := client.resolveDesktopSessionByLabelWithOptions(ctx, resolvedInstance, label, resolveOpts)
			if resolveErr != nil {
				return "", "", "", true, resolveErr
			}
			return resolvedInstance, resolvedID, resolvedKey, true, nil
		}
		resolvedInstance, resolvedID, resolvedKey, resolveErr := client.resolveDesktopSessionByLabelAnyInstanceWithOptions(ctx, label, resolveOpts)
		if resolveErr != nil {
			return "", "", "", true, resolveErr
		}
		return resolvedInstance, resolvedID, resolvedKey, true, nil
	}

	if chatID != "" {
		resolvedInstance, resolveErr := client.resolveDesktopInstanceName(instance)
		if resolveErr != nil {
			return "", "", "", true, resolveErr
		}
		return resolvedInstance, chatID, normalizeDesktopSessionKeyWithInstance(resolvedInstance, chatID), true, nil
	}

	if !requireChat {
		resolvedInstance, resolveErr := client.resolveDesktopInstanceName(instance)
		if resolveErr != nil {
			return "", "", "", true, resolveErr
		}
		return resolvedInstance, "", "", true, nil
	}
	return "", "", "", true, errors.New("desktop action requires chatId, label, or sessionKey")
}

func maybeExecuteMessageSendDesktop(ctx context.Context, args map[string]any, btc *BridgeToolContext) (bool, string, error) {
	if btc == nil || btc.Client == nil {
		return false, "", nil
	}
	uploadID := firstNonEmptyString(args["uploadID"])
	mediaInput := firstNonEmptyString(args["media"], args["path"])
	bufferInput := firstNonEmptyString(args["buffer"])
	shouldRouteDesktop := hasDesktopMessageTargetHints(args) || uploadID != "" || mediaInput != "" || bufferInput != ""
	if !shouldRouteDesktop {
		return false, "", nil
	}

	instance, chatID, key, resolved, err := resolveDesktopMessageTarget(ctx, btc.Client, args, true)
	if !resolved {
		return false, "", nil
	}
	if err != nil {
		return true, "", err
	}

	attachmentUploadID := uploadID
	if attachmentUploadID == "" && (mediaInput != "" || bufferInput != "") {
		data, detectedMime, mediaErr := resolveMessageMedia(ctx, btc, bufferInput, mediaInput)
		if mediaErr != nil {
			return true, "", mediaErr
		}
		mimeType := normalizeMimeString(firstNonEmptyString(args["mimeType"], detectedMime))
		fileName := resolveMessageFilename(args, mediaInput, mimeType)
		uploadResp, uploadErr := btc.Client.uploadDesktopAssetBase64(ctx, instance, data, fileName, mimeType)
		if uploadErr != nil {
			return true, "", fmt.Errorf("failed to upload desktop asset: %w", uploadErr)
		}
		if uploadResp == nil || strings.TrimSpace(uploadResp.UploadID) == "" {
			return true, "", errors.New("desktop asset upload did not return an upload ID")
		}
		attachmentUploadID = strings.TrimSpace(uploadResp.UploadID)
	}

	pendingID, sendErr := btc.Client.sendDesktopMessage(ctx, instance, chatID, desktopSendMessageRequest{
		Text:             firstNonEmptyString(args["message"]),
		ReplyToMessageID: firstNonEmptyString(args["message_id"]),
		Attachment: func() *desktopSendAttachment {
			if attachmentUploadID == "" {
				return nil
			}
			return &desktopSendAttachment{
				UploadID: attachmentUploadID,
				Type:     firstNonEmptyString(args["attachmentType"]),
			}
		}(),
	})
	if sendErr != nil {
		return true, "", sendErr
	}

	result := map[string]any{
		"status":           "sent",
		"channel":          channelDesktopAPI,
		"instance":         instance,
		"chatId":           chatID,
		"sessionKey":       key,
		"pendingMessageId": pendingID,
	}
	if attachmentUploadID != "" {
		result["uploadID"] = attachmentUploadID
	}
	output, err := jsonActionResult("send", result)
	return true, output, err
}

func maybeExecuteMessageEditDesktop(ctx context.Context, args map[string]any, btc *BridgeToolContext) (bool, string, error) {
	if btc == nil || btc.Client == nil {
		return false, "", nil
	}
	if !hasDesktopMessageTargetHints(args) {
		return false, "", nil
	}
	instance, chatID, key, resolved, err := resolveDesktopMessageTarget(ctx, btc.Client, args, true)
	if !resolved {
		return false, "", nil
	}
	if err != nil {
		return true, "", err
	}
	messageID := firstNonEmptyString(args["message_id"])
	if messageID == "" {
		return true, "", errors.New("action=edit requires 'message_id'")
	}
	message := firstNonEmptyString(args["message"])
	if message == "" {
		return true, "", errors.New("action=edit requires 'message'")
	}
	if err := btc.Client.editDesktopMessage(ctx, instance, chatID, messageID, message); err != nil {
		return true, "", err
	}
	output, err := jsonActionResult("edit", map[string]any{
		"status":     "sent",
		"channel":    channelDesktopAPI,
		"instance":   instance,
		"chatId":     chatID,
		"sessionKey": key,
		"messageId":  messageID,
	})
	return true, output, err
}

func maybeExecuteMessageReplyDesktop(ctx context.Context, args map[string]any, btc *BridgeToolContext) (bool, string, error) {
	if btc == nil || btc.Client == nil {
		return false, "", nil
	}
	if !hasDesktopMessageTargetHints(args) {
		return false, "", nil
	}
	instance, chatID, key, resolved, err := resolveDesktopMessageTarget(ctx, btc.Client, args, true)
	if !resolved {
		return false, "", nil
	}
	if err != nil {
		return true, "", err
	}
	replyTo := firstNonEmptyString(args["message_id"])
	if replyTo == "" {
		return true, "", errors.New("action=reply requires 'message_id'")
	}
	text := firstNonEmptyString(args["message"])
	if text == "" {
		return true, "", errors.New("action=reply requires 'message'")
	}
	pendingID, sendErr := btc.Client.sendDesktopMessage(ctx, instance, chatID, desktopSendMessageRequest{
		Text:             text,
		ReplyToMessageID: replyTo,
	})
	if sendErr != nil {
		return true, "", sendErr
	}
	output, err := jsonActionResult("reply", map[string]any{
		"status":           "sent",
		"channel":          channelDesktopAPI,
		"instance":         instance,
		"chatId":           chatID,
		"sessionKey":       key,
		"pendingMessageId": pendingID,
		"replyTo":          replyTo,
	})
	return true, output, err
}

func maybeExecuteMessageSearchDesktop(ctx context.Context, args map[string]any, btc *BridgeToolContext) (bool, string, error) {
	if btc == nil || btc.Client == nil {
		return false, "", nil
	}
	if !hasDesktopMessageTargetHints(args) {
		return false, "", nil
	}
	query := strings.TrimSpace(firstNonEmptyString(args["query"]))
	if query == "" {
		return true, "", errors.New("action=search requires 'query'")
	}
	instance, chatID, _, resolved, err := resolveDesktopMessageTarget(ctx, btc.Client, args, false)
	if !resolved {
		return false, "", nil
	}
	if err != nil {
		return true, "", err
	}
	limit := 20
	if raw, ok := args["limit"].(float64); ok && raw > 0 {
		limit = int(raw)
	}
	messages, searchErr := btc.Client.searchDesktopMessages(ctx, instance, query, limit, chatID)
	if searchErr != nil {
		return true, "", searchErr
	}
	accounts := map[string]beeperdesktopapi.Account{}
	if accountMap, err := btc.Client.listDesktopAccounts(ctx, instance); err == nil && accountMap != nil {
		accounts = accountMap
	}
	result := map[string]any{
		"status":   "ok",
		"channel":  channelDesktopAPI,
		"instance": instance,
		"query":    query,
		"results": buildDesktopSessionMessages(messages, desktopMessageBuildOptions{
			IsGroup:  chatID == "",
			Instance: instance,
			Accounts: accounts,
		}),
		"count": len(messages),
	}
	if chatID != "" {
		result["chatId"] = chatID
		result["sessionKey"] = normalizeDesktopSessionKeyWithInstance(instance, chatID)
	}
	output, err := jsonActionResult("search", result)
	return true, output, err
}

func executeMessageDesktopListChats(ctx context.Context, args map[string]any, btc *BridgeToolContext) (string, error) {
	instance := firstNonEmptyString(args["instance"])
	resolvedInstance, err := btc.Client.resolveDesktopInstanceName(instance)
	if err != nil {
		return "", err
	}
	instance = resolvedInstance
	limit := 50
	if raw, ok := args["limit"].(float64); ok && raw > 0 {
		limit = int(raw)
	}
	chats, err := btc.Client.listDesktopChats(ctx, instance, limit)
	if err != nil {
		return "", err
	}
	return jsonActionResult("desktop-list-chats", map[string]any{
		"status":   "ok",
		"channel":  channelDesktopAPI,
		"instance": instance,
		"chats":    chats,
		"count":    len(chats),
	})
}

func executeMessageDesktopSearchChats(ctx context.Context, args map[string]any, btc *BridgeToolContext) (string, error) {
	query := strings.TrimSpace(firstNonEmptyString(args["query"]))
	if query == "" {
		return "", errors.New("action=desktop-search-chats requires 'query'")
	}
	instance := firstNonEmptyString(args["instance"])
	resolvedInstance, err := btc.Client.resolveDesktopInstanceName(instance)
	if err != nil {
		return "", err
	}
	instance = resolvedInstance
	limit := 50
	if raw, ok := args["limit"].(float64); ok && raw > 0 {
		limit = int(raw)
	}
	chats, err := btc.Client.searchDesktopChats(ctx, instance, query, limit)
	if err != nil {
		return "", err
	}
	return jsonActionResult("desktop-search-chats", map[string]any{
		"status":   "ok",
		"channel":  channelDesktopAPI,
		"instance": instance,
		"query":    query,
		"chats":    chats,
		"count":    len(chats),
	})
}

func executeMessageDesktopSearchMessages(ctx context.Context, args map[string]any, btc *BridgeToolContext) (string, error) {
	query := strings.TrimSpace(firstNonEmptyString(args["query"]))
	if query == "" {
		return "", errors.New("action=desktop-search-messages requires 'query'")
	}
	instance, chatID, key, resolved, err := resolveDesktopMessageTarget(ctx, btc.Client, args, false)
	if err != nil {
		return "", err
	}
	if !resolved {
		resolvedInstance, err := btc.Client.resolveDesktopInstanceName("")
		if err != nil {
			return "", err
		}
		instance = resolvedInstance
	}
	limit := 20
	if raw, ok := args["limit"].(float64); ok && raw > 0 {
		limit = int(raw)
	}
	messages, err := btc.Client.searchDesktopMessages(ctx, instance, query, limit, chatID)
	if err != nil {
		return "", err
	}
	accounts := map[string]beeperdesktopapi.Account{}
	if accountMap, err := btc.Client.listDesktopAccounts(ctx, instance); err == nil && accountMap != nil {
		accounts = accountMap
	}
	payload := map[string]any{
		"status":   "ok",
		"channel":  channelDesktopAPI,
		"instance": instance,
		"query":    query,
		"messages": buildDesktopSessionMessages(messages, desktopMessageBuildOptions{
			IsGroup:  chatID == "",
			Instance: instance,
			Accounts: accounts,
		}),
		"count": len(messages),
	}
	if chatID != "" {
		payload["chatId"] = chatID
		payload["sessionKey"] = key
	}
	return jsonActionResult("desktop-search-messages", payload)
}

func executeMessageDesktopCreateChat(ctx context.Context, args map[string]any, btc *BridgeToolContext) (string, error) {
	instance := firstNonEmptyString(args["instance"])
	resolvedInstance, err := btc.Client.resolveDesktopInstanceName(instance)
	if err != nil {
		return "", err
	}
	instance = resolvedInstance
	accountID := firstNonEmptyString(args["accountId"])
	if accountID == "" {
		return "", errors.New("action=desktop-create-chat requires 'accountId'")
	}
	rawParticipants, ok := args["participantIds"].([]any)
	if !ok || len(rawParticipants) == 0 {
		if arr, ok := args["participantIds"].([]string); ok && len(arr) > 0 {
			rawParticipants = make([]any, 0, len(arr))
			for _, id := range arr {
				rawParticipants = append(rawParticipants, id)
			}
		}
	}
	if len(rawParticipants) == 0 {
		return "", errors.New("action=desktop-create-chat requires 'participantIds'")
	}
	participantIDs := make([]string, 0, len(rawParticipants))
	for _, raw := range rawParticipants {
		if id, ok := raw.(string); ok && strings.TrimSpace(id) != "" {
			participantIDs = append(participantIDs, strings.TrimSpace(id))
		}
	}
	if len(participantIDs) == 0 {
		return "", errors.New("participantIds must contain non-empty strings")
	}
	chatID, err := btc.Client.createDesktopChat(
		ctx,
		instance,
		accountID,
		participantIDs,
		firstNonEmptyString(args["type"]),
		firstNonEmptyString(args["name"]),
		firstNonEmptyString(args["message"]),
	)
	if err != nil {
		return "", err
	}
	return jsonActionResult("desktop-create-chat", map[string]any{
		"status":     "ok",
		"channel":    channelDesktopAPI,
		"instance":   instance,
		"chatId":     chatID,
		"sessionKey": normalizeDesktopSessionKeyWithInstance(instance, chatID),
	})
}

func executeMessageDesktopArchiveChat(ctx context.Context, args map[string]any, btc *BridgeToolContext) (string, error) {
	instance, chatID, key, _, err := resolveDesktopMessageTarget(ctx, btc.Client, args, true)
	if err != nil {
		return "", err
	}
	archived := true
	if raw, ok := args["archived"].(bool); ok {
		archived = raw
	}
	if err := btc.Client.archiveDesktopChat(ctx, instance, chatID, archived); err != nil {
		return "", err
	}
	return jsonActionResult("desktop-archive-chat", map[string]any{
		"status":     "ok",
		"channel":    channelDesktopAPI,
		"instance":   instance,
		"chatId":     chatID,
		"sessionKey": key,
		"archived":   archived,
	})
}

func executeMessageDesktopSetReminder(ctx context.Context, args map[string]any, btc *BridgeToolContext) (string, error) {
	instance, chatID, key, _, err := resolveDesktopMessageTarget(ctx, btc.Client, args, true)
	if err != nil {
		return "", err
	}
	rawRemindAt, ok := args["remindAtMs"].(float64)
	if !ok || rawRemindAt <= 0 {
		return "", errors.New("action=desktop-set-reminder requires numeric 'remindAtMs'")
	}
	dismiss := false
	if raw, ok := args["dismissOnIncomingMessage"].(bool); ok {
		dismiss = raw
	}
	if err := btc.Client.setDesktopChatReminder(ctx, instance, chatID, int64(rawRemindAt), dismiss); err != nil {
		return "", err
	}
	return jsonActionResult("desktop-set-reminder", map[string]any{
		"status":                   "ok",
		"channel":                  channelDesktopAPI,
		"instance":                 instance,
		"chatId":                   chatID,
		"sessionKey":               key,
		"remindAtMs":               int64(rawRemindAt),
		"dismissOnIncomingMessage": dismiss,
	})
}

func executeMessageDesktopClearReminder(ctx context.Context, args map[string]any, btc *BridgeToolContext) (string, error) {
	instance, chatID, key, _, err := resolveDesktopMessageTarget(ctx, btc.Client, args, true)
	if err != nil {
		return "", err
	}
	if err := btc.Client.clearDesktopChatReminder(ctx, instance, chatID); err != nil {
		return "", err
	}
	return jsonActionResult("desktop-clear-reminder", map[string]any{
		"status":     "ok",
		"channel":    channelDesktopAPI,
		"instance":   instance,
		"chatId":     chatID,
		"sessionKey": key,
	})
}

func executeMessageDesktopUploadAsset(ctx context.Context, args map[string]any, btc *BridgeToolContext) (string, error) {
	instance := firstNonEmptyString(args["instance"])
	resolvedInstance, err := btc.Client.resolveDesktopInstanceName(instance)
	if err != nil {
		return "", err
	}
	instance = resolvedInstance
	bufferInput := firstNonEmptyString(args["buffer"])
	mediaInput := firstNonEmptyString(args["media"], args["path"])
	if bufferInput == "" && mediaInput == "" {
		return "", errors.New("action=desktop-upload-asset requires 'buffer' or 'media'")
	}
	data, detectedMime, err := resolveMessageMedia(ctx, btc, bufferInput, mediaInput)
	if err != nil {
		return "", err
	}
	mimeType := normalizeMimeString(firstNonEmptyString(args["mimeType"], detectedMime))
	fileName := resolveMessageFilename(args, mediaInput, mimeType)
	upload, err := btc.Client.uploadDesktopAssetBase64(ctx, instance, data, fileName, mimeType)
	if err != nil {
		return "", err
	}
	return jsonActionResult("desktop-upload-asset", map[string]any{
		"status":   "ok",
		"channel":  channelDesktopAPI,
		"instance": instance,
		"asset":    upload,
	})
}

func executeMessageDesktopDownloadAsset(ctx context.Context, args map[string]any, btc *BridgeToolContext) (string, error) {
	instance := firstNonEmptyString(args["instance"])
	resolvedInstance, err := btc.Client.resolveDesktopInstanceName(instance)
	if err != nil {
		return "", err
	}
	instance = resolvedInstance
	rawURL := firstNonEmptyString(args["url"])
	if rawURL == "" {
		return "", errors.New("action=desktop-download-asset requires 'url'")
	}
	download, err := btc.Client.downloadDesktopAsset(ctx, instance, rawURL)
	if err != nil {
		return "", err
	}
	return jsonActionResult("desktop-download-asset", map[string]any{
		"status":   "ok",
		"channel":  channelDesktopAPI,
		"instance": instance,
		"asset":    download,
	})
}
