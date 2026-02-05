package connector

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	beeperdesktopapi "github.com/beeper/desktop-api-go"
	"github.com/google/uuid"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/ai-bridge/pkg/agents/tools"
)

type sessionListEntry struct {
	updatedAt int64
	data      map[string]any
}

func (oc *AIClient) executeSessionsList(ctx context.Context, portal *bridgev2.Portal, args map[string]any) (*tools.Result, error) {
	kindsRaw := tools.ReadStringArray(args, "kinds")
	allowedKinds := make(map[string]struct{})
	for _, kind := range kindsRaw {
		key := strings.ToLower(strings.TrimSpace(kind))
		if key == "main" || key == "group" || key == "cron" || key == "hook" || key == "node" || key == "other" {
			allowedKinds[key] = struct{}{}
		}
	}
	limit := 50
	if v, err := tools.ReadInt(args, "limit", false); err == nil && v > 0 {
		limit = v
	}
	activeMinutes := 0
	if v, err := tools.ReadInt(args, "activeMinutes", false); err == nil && v > 0 {
		activeMinutes = v
	}
	messageLimit := 0
	if v, err := tools.ReadInt(args, "messageLimit", false); err == nil && v > 0 {
		messageLimit = v
		if messageLimit > 20 {
			messageLimit = 20
		}
	}
	trace := traceEnabled(portalMeta(portal))
	if trace {
		oc.log.Debug().
			Int("limit", limit).
			Int("active_minutes", activeMinutes).
			Int("message_limit", messageLimit).
			Int("kind_filters", len(allowedKinds)).
			Msg("Sessions list requested")
	}

	portals, err := oc.listAllChatPortals(ctx)
	if err != nil {
		return tools.JSONResult(map[string]any{
			"status": "error",
			"error":  err.Error(),
		}), nil
	}

	currentRoomID := id.RoomID("")
	if portal != nil {
		currentRoomID = portal.MXID
	}

	entries := make([]sessionListEntry, 0, len(portals))
	for _, candidate := range portals {
		if candidate == nil || candidate.MXID == "" {
			continue
		}
		meta := portalMeta(candidate)
		if meta != nil && (meta.IsAgentDataRoom || meta.IsGlobalMemoryRoom || meta.IsCronRoom) {
			continue
		}
		kind := resolveSessionKind(currentRoomID, candidate, meta)
		if len(allowedKinds) > 0 {
			if _, ok := allowedKinds[kind]; !ok {
				continue
			}
		}

		updatedAt := int64(0)
		if activeMinutes > 0 || messageLimit > 0 {
			if last := oc.lastMessageTimestamp(ctx, candidate); last > 0 {
				updatedAt = last
			}
			if activeMinutes > 0 {
				cutoff := time.Now().Add(-time.Duration(activeMinutes) * time.Minute).UnixMilli()
				if updatedAt == 0 || updatedAt < cutoff {
					continue
				}
			}
		}

		entry := map[string]any{
			"key":     candidate.MXID.String(),
			"kind":    kind,
			"channel": "matrix",
		}
		if label := resolveSessionLabel(candidate, meta); label != "" {
			entry["label"] = label
		}
		if displayName := resolveSessionDisplayName(candidate, meta); displayName != "" {
			entry["displayName"] = displayName
		}
		if updatedAt > 0 {
			entry["updatedAt"] = updatedAt
		}
		if meta != nil {
			model := meta.Model
			if strings.TrimSpace(model) == "" {
				model = oc.effectiveModel(meta)
			}
			if model != "" {
				entry["model"] = model
			}
			if meta.IsOpenCodeRoom {
				entry["source"] = "opencode"
				if meta.OpenCodeInstanceID != "" {
					entry["opencodeInstanceID"] = meta.OpenCodeInstanceID
				}
				if meta.OpenCodeSessionID != "" {
					entry["opencodeSessionID"] = meta.OpenCodeSessionID
				}
			}
		}
		entry["sessionId"] = string(candidate.PortalKey.ID)

		if messageLimit > 0 {
			messages, err := oc.UserLogin.Bridge.DB.Message.GetLastNInPortal(ctx, candidate.PortalKey, messageLimit)
			if err == nil && len(messages) > 0 {
				entry["messages"] = buildSessionMessages(messages, false)
			}
		}

		entries = append(entries, sessionListEntry{updatedAt: updatedAt, data: entry})
	}

	if oc != nil {
		instances := oc.desktopAPIInstanceNames()
		for _, instance := range instances {
			accounts := map[string]beeperdesktopapi.Account{}
			if accountMap, err := oc.listDesktopAccounts(ctx, instance); err == nil && accountMap != nil {
				accounts = accountMap
			} else if err != nil {
				oc.log.Warn().Err(err).Str("instance", instance).Msg("Desktop API account listing failed")
			}
			desktopEntries, err := oc.listDesktopSessions(ctx, instance, desktopSessionListOptions{
				Limit:         limit,
				ActiveMinutes: activeMinutes,
				MessageLimit:  messageLimit,
				AllowedKinds:  allowedKinds,
			}, accounts)
			if err == nil {
				if len(desktopEntries) > 0 {
					entries = append(entries, desktopEntries...)
				}
			} else {
				oc.log.Warn().Err(err).Str("instance", instance).Msg("Desktop API session listing failed")
			}
		}
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].updatedAt > entries[j].updatedAt
	})
	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}

	result := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		result = append(result, entry.data)
	}
	if trace {
		oc.log.Debug().Int("count", len(result)).Msg("Sessions list completed")
	}

	return tools.JSONResult(map[string]any{
		"sessions": result,
		"count":    len(result),
	}), nil
}

func (oc *AIClient) executeSessionsHistory(ctx context.Context, portal *bridgev2.Portal, args map[string]any) (*tools.Result, error) {
	sessionKey, err := tools.ReadString(args, "sessionKey", true)
	if err != nil || sessionKey == "" {
		return tools.JSONResult(map[string]any{
			"status": "error",
			"error":  "sessionKey is required",
		}), nil
	}
	limit := 50
	if v, err := tools.ReadInt(args, "limit", false); err == nil && v > 0 {
		limit = v
	}
	trace := traceEnabled(portalMeta(portal))
	if trace {
		oc.log.Debug().Str("session_key", sessionKey).Int("limit", limit).Msg("Sessions history requested")
	}

	if instance, chatID, ok := parseDesktopSessionKey(sessionKey); ok {
		if trace {
			oc.log.Debug().Str("instance", instance).Str("chat_id", chatID).Msg("Fetching desktop session history")
		}
		client, clientErr := oc.desktopAPIClient(instance)
		if clientErr != nil || client == nil {
			if clientErr == nil {
				clientErr = fmt.Errorf("desktop API token is not set")
			}
			return tools.JSONResult(map[string]any{
				"status": "error",
				"error":  clientErr.Error(),
			}), nil
		}
		chat, chatErr := client.Chats.Get(ctx, chatID, beeperdesktopapi.ChatGetParams{})
		if chatErr != nil {
			oc.log.Warn().Err(chatErr).Str("instance", instance).Msg("Desktop API chat lookup failed")
		}
		accounts := map[string]beeperdesktopapi.Account{}
		if accountMap, err := oc.listDesktopAccounts(ctx, instance); err == nil && accountMap != nil {
			accounts = accountMap
		} else if err != nil {
			oc.log.Warn().Err(err).Str("instance", instance).Msg("Desktop API account listing failed")
		}
		messages, msgErr := oc.listDesktopMessages(ctx, client, chatID, limit)
		if msgErr != nil {
			return tools.JSONResult(map[string]any{
				"status": "error",
				"error":  msgErr.Error(),
			}), nil
		}
		baseURL := ""
		if config, ok := oc.desktopAPIInstanceConfig(instance); ok {
			baseURL = strings.TrimSpace(config.BaseURL)
		}
		isGroup := true
		if chat != nil && chat.Type == beeperdesktopapi.ChatTypeSingle {
			isGroup = false
		}
		payload := map[string]any{
			"sessionKey": normalizeDesktopSessionKeyWithInstance(instance, chatID),
			"messages": buildDesktopSessionMessages(messages, desktopMessageBuildOptions{
				IsGroup:  isGroup,
				Instance: instance,
				BaseURL:  baseURL,
				Accounts: accounts,
			}),
			"channel":  channelDesktopAPI,
			"instance": instance,
			"chatId":   chatID,
		}
		if baseURL != "" {
			payload["baseUrl"] = baseURL
		}
		if chat != nil {
			payload["chat"] = chat
			if title := strings.TrimSpace(chat.Title); title != "" {
				payload["label"] = title
				payload["displayName"] = title
			}
			if accountID := strings.TrimSpace(chat.AccountID); accountID != "" {
				payload["accountId"] = accountID
				if account, ok := accounts[accountID]; ok {
					payload["account"] = account
					if network := strings.TrimSpace(account.Network); network != "" {
						payload["network"] = network
					}
					payload["accountUser"] = account.User
				}
			}
			if chat.Type != "" {
				payload["chatType"] = string(chat.Type)
			}
		} else if len(messages) > 0 {
			accountID := strings.TrimSpace(messages[len(messages)-1].AccountID)
			if accountID != "" {
				payload["accountId"] = accountID
				if account, ok := accounts[accountID]; ok {
					payload["account"] = account
					if network := strings.TrimSpace(account.Network); network != "" {
						payload["network"] = network
					}
					payload["accountUser"] = account.User
				}
			}
		}
		return tools.JSONResult(payload), nil
	}

	resolvedPortal, displayKey, resolveErr := oc.resolveSessionPortal(ctx, portal, sessionKey)
	if resolveErr != nil {
		return tools.JSONResult(map[string]any{
			"status": "error",
			"error":  resolveErr.Error(),
		}), nil
	}

	messages, err := oc.UserLogin.Bridge.DB.Message.GetLastNInPortal(ctx, resolvedPortal.PortalKey, limit)
	if err != nil {
		return tools.JSONResult(map[string]any{
			"status": "error",
			"error":  err.Error(),
		}), nil
	}
	if trace {
		oc.log.Debug().Int("count", len(messages)).Msg("Sessions history fetched from Matrix")
	}

	includeTools := false
	if raw, ok := args["includeTools"]; ok {
		if value, ok := raw.(bool); ok {
			includeTools = value
		}
	}

	return tools.JSONResult(map[string]any{
		"sessionKey": displayKey,
		"messages":   buildSessionMessages(messages, includeTools),
	}), nil
}

func (oc *AIClient) executeSessionsSend(ctx context.Context, portal *bridgev2.Portal, args map[string]any) (*tools.Result, error) {
	message, err := tools.ReadString(args, "message", true)
	if err != nil || strings.TrimSpace(message) == "" {
		return tools.JSONResult(map[string]any{
			"status": "error",
			"error":  "message is required",
		}), nil
	}
	meta := portalMeta(portal)
	trace := traceEnabled(meta)
	traceFull := traceFull(meta)
	if trace {
		if portal != nil {
			oc.log.Debug().Stringer("portal", portal.PortalKey).Msg("Sessions send requested")
		} else {
			oc.log.Debug().Msg("Sessions send requested")
		}
		oc.log.Debug().Int("message_len", len(strings.TrimSpace(message))).Msg("Sessions send message length")
	}
	if traceFull {
		oc.log.Debug().Str("message", strings.TrimSpace(message)).Msg("Sessions send body")
	}
	sessionKey := tools.ReadStringDefault(args, "sessionKey", "")
	label := tools.ReadStringDefault(args, "label", "")
	agentID := tools.ReadStringDefault(args, "agentId", "")
	instance := tools.ReadStringDefault(args, "instance", "")
	if sessionKey != "" && label != "" {
		return tools.JSONResult(map[string]any{
			"status": "error",
			"error":  "Provide either sessionKey or label (not both).",
		}), nil
	}

	if instance, chatID, ok := parseDesktopSessionKey(sessionKey); ok {
		if trace {
			oc.log.Debug().Str("instance", instance).Str("chat_id", chatID).Msg("Sending to desktop session by key")
		}
		pendingID, sendErr := oc.sendDesktopMessage(ctx, instance, chatID, message)
		if sendErr != nil {
			return tools.JSONResult(map[string]any{
				"status": "error",
				"error":  sendErr.Error(),
			}), nil
		}
		baseURL := ""
		if config, ok := oc.desktopAPIInstanceConfig(instance); ok {
			baseURL = strings.TrimSpace(config.BaseURL)
		}
		result := map[string]any{
			"runId":            uuid.NewString(),
			"status":           "ok",
			"sessionKey":       normalizeDesktopSessionKeyWithInstance(instance, chatID),
			"pendingMessageId": pendingID,
			"instance":         instance,
			"chatId":           chatID,
		}
		if baseURL != "" {
			result["baseUrl"] = baseURL
		}
		return tools.JSONResult(result), nil
	}

	var targetPortal *bridgev2.Portal
	var displayKey string
	if sessionKey != "" {
		target, display, resolveErr := oc.resolveSessionPortal(ctx, portal, sessionKey)
		if resolveErr != nil {
			return tools.JSONResult(map[string]any{
				"status": "error",
				"error":  resolveErr.Error(),
			}), nil
		}
		targetPortal = target
		displayKey = display
		if trace {
			oc.log.Debug().Stringer("portal", targetPortal.PortalKey).Msg("Resolved session key to Matrix portal")
		}
	} else {
		if strings.TrimSpace(label) == "" {
			return tools.JSONResult(map[string]any{
				"status": "error",
				"error":  "sessionKey or label is required",
			}), nil
		}
		target, display, resolveErr := oc.resolveSessionPortalByLabel(ctx, label, agentID)
		if resolveErr != nil {
			var desktopInstance string
			var chatID string
			var desktopKey string
			var desktopErr error
			if strings.TrimSpace(instance) != "" {
				chatID, desktopKey, desktopErr = oc.resolveDesktopSessionByLabel(ctx, instance, label)
				desktopInstance = instance
			} else {
				desktopInstance, chatID, desktopKey, desktopErr = oc.resolveDesktopSessionByLabelAnyInstance(ctx, label)
			}
			if desktopErr == nil {
				if trace {
					oc.log.Debug().Str("instance", desktopInstance).Str("chat_id", chatID).Msg("Sending to desktop session by label")
				}
				pendingID, sendErr := oc.sendDesktopMessage(ctx, desktopInstance, chatID, message)
				if sendErr != nil {
					return tools.JSONResult(map[string]any{
						"status": "error",
						"error":  sendErr.Error(),
					}), nil
				}
				baseURL := ""
				if config, ok := oc.desktopAPIInstanceConfig(desktopInstance); ok {
					baseURL = strings.TrimSpace(config.BaseURL)
				}
				result := map[string]any{
					"runId":            uuid.NewString(),
					"status":           "ok",
					"sessionKey":       desktopKey,
					"pendingMessageId": pendingID,
					"instance":         desktopInstance,
					"chatId":           chatID,
				}
				if baseURL != "" {
					result["baseUrl"] = baseURL
				}
				return tools.JSONResult(result), nil
			}
			if desktopErr != nil {
				return tools.JSONResult(map[string]any{
					"status": "error",
					"error":  desktopErr.Error(),
				}), nil
			}
			return tools.JSONResult(map[string]any{
				"status": "error",
				"error":  resolveErr.Error(),
			}), nil
		}
		targetPortal = target
		displayKey = display
		if trace {
			oc.log.Debug().Stringer("portal", targetPortal.PortalKey).Msg("Resolved session label to Matrix portal")
		}
	}

	if targetPortal == nil {
		return tools.JSONResult(map[string]any{
			"status": "error",
			"error":  "session not found",
		}), nil
	}

	queued := false
	if _, queuedFlag, dispatchErr := oc.dispatchInternalMessage(ctx, targetPortal, portalMeta(targetPortal), message, "sessions-send", false); dispatchErr != nil {
		return tools.JSONResult(map[string]any{
			"status": "error",
			"error":  dispatchErr.Error(),
		}), nil
	} else {
		queued = queuedFlag
	}
	if trace {
		oc.log.Debug().Bool("queued", queued).Msg("Sessions send dispatched")
	}

	status := "ok"
	if queued {
		status = "accepted"
	}

	return tools.JSONResult(map[string]any{
		"runId":      uuid.NewString(),
		"status":     status,
		"sessionKey": displayKey,
	}), nil
}

func resolveSessionKind(current id.RoomID, portal *bridgev2.Portal, meta *PortalMetadata) string {
	if current != "" && portal != nil && portal.MXID == current {
		return "main"
	}
	if meta != nil {
		if meta.IsCronRoom {
			return "cron"
		}
		if strings.TrimSpace(meta.SubagentParentRoomID) != "" {
			return "other"
		}
		if meta.IsBuilderRoom {
			return "other"
		}
	}
	return "group"
}

func resolveSessionLabel(portal *bridgev2.Portal, meta *PortalMetadata) string {
	if meta != nil {
		if strings.TrimSpace(meta.Title) != "" {
			return strings.TrimSpace(meta.Title)
		}
	}
	if portal != nil && strings.TrimSpace(portal.Name) != "" {
		return strings.TrimSpace(portal.Name)
	}
	return ""
}

func resolveSessionDisplayName(portal *bridgev2.Portal, meta *PortalMetadata) string {
	if portal != nil && strings.TrimSpace(portal.Name) != "" {
		return strings.TrimSpace(portal.Name)
	}
	if meta != nil {
		return strings.TrimSpace(meta.Title)
	}
	return ""
}

func (oc *AIClient) resolveSessionPortal(ctx context.Context, portal *bridgev2.Portal, sessionKey string) (*bridgev2.Portal, string, error) {
	trimmed := strings.TrimSpace(sessionKey)
	if trimmed == "" {
		return nil, "", fmt.Errorf("sessionKey is required")
	}
	if trimmed == "main" {
		if portal == nil || portal.MXID == "" {
			return nil, "", fmt.Errorf("main session not available")
		}
		return portal, "main", nil
	}
	if strings.HasPrefix(trimmed, "!") {
		if found, err := oc.UserLogin.Bridge.GetPortalByMXID(ctx, id.RoomID(trimmed)); err == nil && found != nil {
			return found, found.MXID.String(), nil
		}
	}
	portals, err := oc.listAllChatPortals(ctx)
	if err != nil {
		return nil, "", err
	}
	for _, candidate := range portals {
		if candidate == nil {
			continue
		}
		if candidate.MXID.String() == trimmed || string(candidate.PortalKey.ID) == trimmed {
			key := candidate.MXID.String()
			if key == "" {
				key = trimmed
			}
			return candidate, key, nil
		}
	}
	return nil, "", fmt.Errorf("session not found: %s", trimmed)
}

func (oc *AIClient) resolveSessionPortalByLabel(ctx context.Context, label string, agentID string) (*bridgev2.Portal, string, error) {
	trimmed := strings.TrimSpace(label)
	if trimmed == "" {
		return nil, "", fmt.Errorf("label is required")
	}
	needle := strings.ToLower(trimmed)
	filterAgent := normalizeAgentID(agentID)

	portals, err := oc.listAllChatPortals(ctx)
	if err != nil {
		return nil, "", err
	}
	for _, candidate := range portals {
		if candidate == nil {
			continue
		}
		meta := portalMeta(candidate)
		if filterAgent != "" {
			agent := normalizeAgentID(resolveAgentID(meta))
			if agent != filterAgent {
				continue
			}
		}
		labelVal := strings.ToLower(resolveSessionLabel(candidate, meta))
		displayVal := strings.ToLower(resolveSessionDisplayName(candidate, meta))
		if labelVal == needle || displayVal == needle {
			key := candidate.MXID.String()
			if key == "" {
				key = string(candidate.PortalKey.ID)
			}
			return candidate, key, nil
		}
	}
	return nil, "", fmt.Errorf("no session found for label '%s'", trimmed)
}

func (oc *AIClient) lastMessageTimestamp(ctx context.Context, portal *bridgev2.Portal) int64 {
	if portal == nil {
		return 0
	}
	messages, err := oc.UserLogin.Bridge.DB.Message.GetLastNInPortal(ctx, portal.PortalKey, 1)
	if err != nil || len(messages) == 0 {
		return 0
	}
	return messages[len(messages)-1].Timestamp.UnixMilli()
}

func buildSessionMessages(messages []*database.Message, includeTools bool) []map[string]any {
	result := make([]map[string]any, 0, len(messages))
	for _, msg := range messages {
		meta := messageMeta(msg)
		if meta == nil {
			continue
		}
		if !includeTools && meta.Role != "assistant" && meta.Role != "user" {
			continue
		}
		if includeTools && meta.Role != "assistant" && meta.Role != "user" && meta.Role != "tool" {
			continue
		}
		entry := map[string]any{
			"role":      meta.Role,
			"content":   meta.Body,
			"timestamp": msg.Timestamp.UnixMilli(),
		}
		if msg.MXID != "" {
			entry["id"] = msg.MXID.String()
		}
		result = append(result, entry)
	}
	return result
}
