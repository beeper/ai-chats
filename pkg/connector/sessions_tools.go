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
	"maunium.net/go/mautrix/id"

	"github.com/beeper/ai-bridge/pkg/agents/tools"
)

type sessionListEntry struct {
	updatedAt int64
	data      map[string]any
}

func shouldExcludeModelVisiblePortal(meta *PortalMetadata) bool {
	if meta == nil {
		return false
	}
	if meta.IsAgentDataRoom || meta.IsGlobalMemoryRoom || meta.IsCronRoom || meta.IsBuilderRoom || meta.IsOpenCodeRoom {
		return true
	}
	return strings.TrimSpace(meta.SubagentParentRoomID) != ""
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
		if shouldExcludeModelVisiblePortal(meta) {
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

		sessionKey := candidate.MXID.String()
		entry := map[string]any{
			"key":     sessionKey,
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
		entry["sessionId"] = sessionKey
		if portalID := string(candidate.PortalKey.ID); portalID != "" && portalID != sessionKey {
			entry["portalId"] = portalID
		}

		if messageLimit > 0 {
			messages, err := oc.UserLogin.Bridge.DB.Message.GetLastNInPortal(ctx, candidate.PortalKey, messageLimit)
			if err == nil && len(messages) > 0 {
				openClawMessages := buildOpenClawSessionMessages(messages, false)
				if len(openClawMessages) > messageLimit {
					openClawMessages = openClawMessages[len(openClawMessages)-messageLimit:]
				}
				entry["messages"] = openClawMessages
			}
		}

		entries = append(entries, sessionListEntry{updatedAt: updatedAt, data: entry})
	}

	if oc != nil {
		instances := oc.desktopAPIInstanceNames()
		hasMultipleDesktopInstances := len(instances) > 1
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
				MultiInstance: hasMultipleDesktopInstances,
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
	rawLimit := 0
	if v, err := tools.ReadInt(args, "limit", false); err == nil && v > 0 {
		rawLimit = v
	}
	limit := normalizeOpenClawHistoryLimit(rawLimit)
	includeTools := false
	if raw, ok := args["includeTools"]; ok {
		if value, ok := raw.(bool); ok {
			includeTools = value
		}
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
		chat, chatErr := client.Chats.Get(ctx, escapeDesktopPathSegment(chatID), beeperdesktopapi.ChatGetParams{})
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
		isGroup := true
		if chat != nil && chat.Type == beeperdesktopapi.ChatTypeSingle {
			isGroup = false
		}
		openClawMessages := buildOpenClawDesktopSessionMessages(messages, desktopMessageBuildOptions{
			IsGroup:  isGroup,
			Instance: instance,
			Accounts: accounts,
		})
		if len(openClawMessages) > limit {
			openClawMessages = openClawMessages[len(openClawMessages)-limit:]
		}
		openClawMessages = capOpenClawHistoryByJSONBytes(openClawMessages, openClawMaxHistoryBytes)
		if !includeTools {
			openClawMessages = stripOpenClawToolResults(openClawMessages)
		}
		return tools.JSONResult(map[string]any{
			"sessionKey": normalizeDesktopSessionKeyWithInstance(instance, chatID),
			"messages":   openClawMessages,
		}), nil
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

	openClawMessages := buildOpenClawSessionMessages(messages, true)
	if len(openClawMessages) > limit {
		openClawMessages = openClawMessages[len(openClawMessages)-limit:]
	}
	openClawMessages = capOpenClawHistoryByJSONBytes(openClawMessages, openClawMaxHistoryBytes)
	if !includeTools {
		openClawMessages = stripOpenClawToolResults(openClawMessages)
	}

	return tools.JSONResult(map[string]any{
		"sessionKey": displayKey,
		"messages":   openClawMessages,
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
	timeoutSeconds := 30
	if v, err := tools.ReadInt(args, "timeoutSeconds", false); err == nil && v >= 0 {
		timeoutSeconds = v
	}
	runID := uuid.NewString()
	if sessionKey != "" && label != "" {
		return tools.JSONResult(map[string]any{
			"runId":  runID,
			"status": "error",
			"error":  "Provide either sessionKey or label (not both).",
		}), nil
	}

	if instance, chatID, ok := parseDesktopSessionKey(sessionKey); ok {
		if trace {
			oc.log.Debug().Str("instance", instance).Str("chat_id", chatID).Msg("Sending to desktop session by key")
		}
		_, sendErr := oc.sendDesktopMessage(ctx, instance, chatID, desktopSendMessageRequest{
			Text: message,
		})
		if sendErr != nil {
			return tools.JSONResult(map[string]any{
				"status": "error",
				"error":  sendErr.Error(),
			}), nil
		}
		result := map[string]any{
			"runId":      runID,
			"status":     "accepted",
			"sessionKey": normalizeDesktopSessionKeyWithInstance(instance, chatID),
			"delivery": map[string]any{
				"status": "pending",
				"mode":   "announce",
			},
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
				_, sendErr := oc.sendDesktopMessage(ctx, desktopInstance, chatID, desktopSendMessageRequest{
					Text: message,
				})
				if sendErr != nil {
					return tools.JSONResult(map[string]any{
						"status": "error",
						"error":  sendErr.Error(),
					}), nil
				}
				result := map[string]any{
					"runId":      runID,
					"status":     "accepted",
					"sessionKey": desktopKey,
					"delivery": map[string]any{
						"status": "pending",
						"mode":   "announce",
					},
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
			"runId":  runID,
			"status": "error",
			"error":  "session not found",
		}), nil
	}

	lastAssistantID, lastAssistantTimestamp := oc.lastAssistantMessageInfo(ctx, targetPortal)
	queued := false
	if dispatchEventID, queuedFlag, dispatchErr := oc.dispatchInternalMessage(ctx, targetPortal, portalMeta(targetPortal), message, "sessions-send", false); dispatchErr != nil {
		status := "error"
		if isForbiddenSessionSendError(dispatchErr.Error()) {
			status = "forbidden"
		}
		return tools.JSONResult(map[string]any{
			"runId":  runID,
			"status": status,
			"error":  dispatchErr.Error(),
		}), nil
	} else {
		if dispatchEventID != "" {
			runID = dispatchEventID.String()
		}
		queued = queuedFlag
	}
	if trace {
		oc.log.Debug().Bool("queued", queued).Msg("Sessions send dispatched")
	}

	delivery := map[string]any{
		"status": "pending",
		"mode":   "announce",
	}
	result := map[string]any{
		"runId":      runID,
		"status":     "accepted",
		"sessionKey": displayKey,
		"delivery":   delivery,
	}
	if timeoutSeconds == 0 {
		return tools.JSONResult(result), nil
	}

	timeout := time.Duration(timeoutSeconds) * time.Second
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		assistantMsg, found := oc.waitForNewAssistantMessage(ctx, targetPortal, lastAssistantID, lastAssistantTimestamp)
		if found {
			reply := ""
			if assistantMsg != nil {
				if assistantMeta := messageMeta(assistantMsg); assistantMeta != nil {
					reply = strings.TrimSpace(assistantMeta.Body)
				}
			}
			result["status"] = "ok"
			result["reply"] = reply
			return tools.JSONResult(result), nil
		}
		time.Sleep(250 * time.Millisecond)
	}

	if trace {
		oc.log.Debug().Bool("queued", queued).Str("session_key", displayKey).Msg("Sessions send timed out waiting for assistant reply")
	}
	result["status"] = "timeout"
	result["error"] = "timeout waiting for assistant reply"
	return tools.JSONResult(result), nil
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

func isForbiddenSessionSendError(errText string) bool {
	text := strings.ToLower(strings.TrimSpace(errText))
	if text == "" {
		return false
	}
	return strings.Contains(text, "forbidden") ||
		strings.Contains(text, "not allowed") ||
		strings.Contains(text, "permission denied") ||
		strings.Contains(text, "restricted")
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
	return nil, "", fmt.Errorf("session not found: %s (use the full sessionKey from sessions_list)", trimmed)
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
	matches := make([]*bridgev2.Portal, 0, 4)
	for _, candidate := range portals {
		if candidate == nil {
			continue
		}
		meta := portalMeta(candidate)
		if shouldExcludeModelVisiblePortal(meta) {
			continue
		}
		if filterAgent != "" {
			agent := normalizeAgentID(resolveAgentID(meta))
			if agent != filterAgent {
				continue
			}
		}
		labelVal := strings.ToLower(resolveSessionLabel(candidate, meta))
		displayVal := strings.ToLower(resolveSessionDisplayName(candidate, meta))
		if labelVal == needle || displayVal == needle {
			matches = append(matches, candidate)
		}
	}
	if len(matches) == 1 {
		key := matches[0].MXID.String()
		if key == "" {
			key = string(matches[0].PortalKey.ID)
		}
		return matches[0], key, nil
	}
	if len(matches) > 1 {
		return nil, "", fmt.Errorf("label '%s' matched multiple sessions; use full sessionKey from sessions_list", trimmed)
	}
	return nil, "", fmt.Errorf("no session found for label '%s' (use full sessionKey from sessions_list)", trimmed)
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
