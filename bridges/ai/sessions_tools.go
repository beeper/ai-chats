package ai

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	beeperdesktopapi "github.com/beeper/desktop-api-go"
	"github.com/google/uuid"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/pkg/agents/tools"
)

type sessionListEntry struct {
	updatedAt int64
	data      map[string]any
}

func shouldExcludeModelVisiblePortal(meta *PortalMetadata) bool {
	if meta == nil {
		return false
	}
	if meta.InternalRoom() {
		return true
	}
	return strings.TrimSpace(meta.SubagentParentRoomID) != ""
}

func (oc *AIClient) executeSessionsList(ctx context.Context, portal *bridgev2.Portal, args map[string]any) (*tools.Result, error) {
	kindsRaw := tools.ReadStringArray(args, "kinds")
	allowedKinds := make(map[string]struct{})
	for _, kind := range kindsRaw {
		key := strings.ToLower(strings.TrimSpace(kind))
		if isIntegrationSessionKindAllowed(key) {
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
	portals, err := oc.listAllChatPortals(ctx)
	if err != nil {
		return tools.JSONErrorResult(err.Error()), nil
	}

	var currentRoomID id.RoomID
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
			messages, err := oc.getAIHistoryMessages(ctx, candidate, 1)
			if err == nil && len(messages) > 0 {
				updatedAt = messages[len(messages)-1].Timestamp.UnixMilli()
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
			"sessionKey": sessionKey,
			"kind":       kind,
			"channel":    "matrix",
		}
		label := ""
		if strings.TrimSpace(candidate.Name) != "" {
			label = strings.TrimSpace(candidate.Name)
		} else if meta != nil && strings.TrimSpace(meta.Slug) != "" {
			label = strings.TrimSpace(meta.Slug)
		}
		if label != "" {
			entry["label"] = label
		}
		displayName := ""
		if strings.TrimSpace(candidate.Name) != "" {
			displayName = strings.TrimSpace(candidate.Name)
		} else if meta != nil {
			displayName = strings.TrimSpace(meta.Slug)
		}
		if displayName != "" {
			entry["displayName"] = displayName
		}
		if updatedAt > 0 {
			entry["updatedAt"] = updatedAt
		}
		if meta != nil {
			if model := oc.effectiveModel(meta); model != "" {
				entry["model"] = model
			}
		}
		if messageLimit > 0 {
			messages, err := oc.getAIHistoryMessages(ctx, candidate, messageLimit)
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

	resultPayload := map[string]any{
		"sessions": nil,
		"count":    0,
	}

	if oc != nil {
		instances := oc.desktopAPIInstanceNames(ctx)
		hasMultipleDesktopInstances := len(instances) > 1
		desktopErrors := make([]map[string]any, 0, 2)
		for _, instance := range instances {
			accounts := map[string]beeperdesktopapi.Account{}
			if accountMap, err := oc.listDesktopAccounts(ctx, instance); err == nil && accountMap != nil {
				accounts = accountMap
			} else if err != nil {
				oc.loggerForContext(ctx).Warn().Err(err).Str("instance", instance).Msg("Desktop API account listing failed")
				desktopErrors = append(desktopErrors, map[string]any{
					"instance": instance,
					"op":       "accounts_list",
					"error":    err.Error(),
				})
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
				oc.loggerForContext(ctx).Warn().Err(err).Str("instance", instance).Msg("Desktop API session listing failed")
				desktopErrors = append(desktopErrors, map[string]any{
					"instance": instance,
					"op":       "sessions_list",
					"error":    err.Error(),
				})
			}
		}

		desktopStatus := map[string]any{
			"configured": len(instances) > 0,
			"instances":  instances,
		}
		if len(desktopErrors) > 0 {
			desktopStatus["errors"] = desktopErrors
		}
		resultPayload["desktopApi"] = desktopStatus
	}

	slices.SortFunc(entries, func(a, b sessionListEntry) int {
		return cmp.Compare(b.updatedAt, a.updatedAt)
	})
	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}

	result := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		result = append(result, entry.data)
	}
	resultPayload["sessions"] = result
	resultPayload["count"] = len(result)
	return tools.JSONResult(resultPayload), nil
}

func (oc *AIClient) executeSessionsHistory(ctx context.Context, portal *bridgev2.Portal, args map[string]any) (*tools.Result, error) {
	sessionKey, err := tools.ReadString(args, "sessionKey", true)
	if err != nil || sessionKey == "" {
		return tools.JSONErrorResult("sessionKey is required"), nil
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
	if instance, chatID, ok := parseDesktopSessionKey(sessionKey); ok {
		resolvedInstance, resolveErr := resolveDesktopInstanceName(oc.desktopAPIInstances(ctx), instance)
		if resolveErr != nil {
			return tools.JSONErrorResult(resolveErr.Error()), nil
		}
		instance = resolvedInstance
		client, clientErr := oc.desktopAPIClient(ctx, instance)
		if clientErr != nil || client == nil {
			if clientErr == nil {
				clientErr = errors.New("desktop API token is not set")
			}
			return tools.JSONErrorResult(clientErr.Error()), nil
		}
		chat, chatErr := client.Chats.Get(ctx, escapeDesktopPathSegment(chatID), beeperdesktopapi.ChatGetParams{})
		if chatErr != nil {
			oc.loggerForContext(ctx).Warn().Err(chatErr).Str("instance", instance).Msg("Desktop API chat lookup failed")
		}
		accounts := map[string]beeperdesktopapi.Account{}
		if accountMap, err := oc.listDesktopAccounts(ctx, instance); err == nil && accountMap != nil {
			accounts = accountMap
		} else if err != nil {
			oc.loggerForContext(ctx).Warn().Err(err).Str("instance", instance).Msg("Desktop API account listing failed")
		}
		messages, msgErr := oc.listDesktopMessages(ctx, client, chatID, limit)
		if msgErr != nil {
			return tools.JSONErrorResult(msgErr.Error()), nil
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

	trimmedSessionKey := strings.TrimSpace(sessionKey)
	if trimmedSessionKey == "" {
		return tools.JSONErrorResult("sessionKey is required"), nil
	}
	var resolvedPortal *bridgev2.Portal
	displayKey := ""
	switch {
	case trimmedSessionKey == "main":
		if portal == nil || portal.MXID == "" {
			return tools.JSONErrorResult("main session not available"), nil
		}
		resolvedPortal = portal
		displayKey = "main"
	case strings.HasPrefix(trimmedSessionKey, "!"):
		if found := oc.portalByRoomID(ctx, id.RoomID(trimmedSessionKey)); found != nil {
			resolvedPortal = found
			displayKey = found.MXID.String()
		}
	default:
		portals, err := oc.listAllChatPortals(ctx)
		if err != nil {
			return tools.JSONErrorResult(err.Error()), nil
		}
		for _, candidate := range portals {
			if candidate == nil {
				continue
			}
			if candidate.MXID.String() == trimmedSessionKey || string(candidate.PortalKey.ID) == trimmedSessionKey {
				resolvedPortal = candidate
				displayKey = candidate.MXID.String()
				if displayKey == "" {
					displayKey = trimmedSessionKey
				}
				break
			}
		}
	}
	if resolvedPortal == nil {
		return tools.JSONErrorResult(fmt.Sprintf("session not found: %s (use the sessionKey from sessions_list)", trimmedSessionKey)), nil
	}

	messages, err := oc.getAIHistoryMessages(ctx, resolvedPortal, limit)
	if err != nil {
		return tools.JSONErrorResult(err.Error()), nil
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
		return tools.JSONErrorResult("message is required"), nil
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
		resolvedInstance, resolveErr := resolveDesktopInstanceName(oc.desktopAPIInstances(ctx), instance)
		if resolveErr != nil {
			return tools.JSONErrorResult(resolveErr.Error()), nil
		}
		instance = resolvedInstance
		_, sendErr := oc.sendDesktopMessage(ctx, instance, chatID, desktopSendMessageRequest{
			Text: message,
		})
		if sendErr != nil {
			return tools.JSONErrorResult(sendErr.Error()), nil
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
		trimmedSessionKey := strings.TrimSpace(sessionKey)
		if trimmedSessionKey == "" {
			return tools.JSONErrorResult("sessionKey is required"), nil
		}
		switch {
		case trimmedSessionKey == "main":
			if portal == nil || portal.MXID == "" {
				return tools.JSONErrorResult("main session not available"), nil
			}
			targetPortal = portal
			displayKey = "main"
		case strings.HasPrefix(trimmedSessionKey, "!"):
			if found := oc.portalByRoomID(ctx, id.RoomID(trimmedSessionKey)); found != nil {
				targetPortal = found
				displayKey = found.MXID.String()
			}
		default:
			portals, err := oc.listAllChatPortals(ctx)
			if err != nil {
				return tools.JSONErrorResult(err.Error()), nil
			}
			for _, candidate := range portals {
				if candidate == nil {
					continue
				}
				if candidate.MXID.String() == trimmedSessionKey || string(candidate.PortalKey.ID) == trimmedSessionKey {
					targetPortal = candidate
					displayKey = candidate.MXID.String()
					if displayKey == "" {
						displayKey = trimmedSessionKey
					}
					break
				}
			}
		}
		if targetPortal == nil {
			return tools.JSONErrorResult(fmt.Sprintf("session not found: %s (use the sessionKey from sessions_list)", trimmedSessionKey)), nil
		}
	} else {
		if strings.TrimSpace(label) == "" {
			return tools.JSONErrorResult("sessionKey or label is required"), nil
		}
		trimmed := strings.TrimSpace(label)
		needle := strings.ToLower(trimmed)
		filterAgent := normalizeAgentID(agentID)
		portals, err := oc.listAllChatPortals(ctx)
		if err != nil {
			return tools.JSONErrorResult(err.Error()), nil
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
			labelVal := ""
			if strings.TrimSpace(candidate.Name) != "" {
				labelVal = strings.ToLower(strings.TrimSpace(candidate.Name))
			} else if meta != nil && strings.TrimSpace(meta.Slug) != "" {
				labelVal = strings.ToLower(strings.TrimSpace(meta.Slug))
			}
			displayVal := ""
			if strings.TrimSpace(candidate.Name) != "" {
				displayVal = strings.ToLower(strings.TrimSpace(candidate.Name))
			} else if meta != nil {
				displayVal = strings.ToLower(strings.TrimSpace(meta.Slug))
			}
			if labelVal == needle || displayVal == needle {
				matches = append(matches, candidate)
			}
		}
		if len(matches) != 1 {
			var desktopInstance string
			var chatID string
			var desktopKey string
			var desktopErr error
			if strings.TrimSpace(instance) != "" {
				resolvedInstance, resolveErr := resolveDesktopInstanceName(oc.desktopAPIInstances(ctx), instance)
				if resolveErr != nil {
					return tools.JSONErrorResult(resolveErr.Error()), nil
				}
				desktopInstance = resolvedInstance
				chatID, desktopKey, desktopErr = oc.resolveDesktopSessionByLabelWithOptions(ctx, resolvedInstance, label, desktopLabelResolveOptions{})
			} else {
				desktopInstance, chatID, desktopKey, desktopErr = oc.resolveDesktopSessionByLabelAnyInstanceWithOptions(ctx, label, desktopLabelResolveOptions{})
			}
			if desktopErr != nil {
				return tools.JSONErrorResult(desktopErr.Error()), nil
			}
			_, sendErr := oc.sendDesktopMessage(ctx, desktopInstance, chatID, desktopSendMessageRequest{
				Text: message,
			})
			if sendErr != nil {
				return tools.JSONErrorResult(sendErr.Error()), nil
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
		targetPortal = matches[0]
		displayKey = targetPortal.MXID.String()
		if displayKey == "" {
			displayKey = string(targetPortal.PortalKey.ID)
		}
	}

	if targetPortal == nil {
		return tools.JSONResult(map[string]any{
			"runId":  runID,
			"status": "error",
			"error":  "session not found",
		}), nil
	}

	lastAssistantCheckpoint := oc.lastAssistantTurnCheckpoint(ctx, targetPortal)
	if dispatchEventID, _, dispatchErr := oc.dispatchInternalMessage(ctx, targetPortal, portalMeta(targetPortal), message, "sessions-send", false); dispatchErr != nil {
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
		assistantMsg, found := oc.waitForAssistantTurnAfter(ctx, targetPortal, lastAssistantCheckpoint)
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

	result["status"] = "timeout"
	result["error"] = "timeout waiting for assistant reply"
	return tools.JSONResult(result), nil
}

func resolveSessionKind(current id.RoomID, portal *bridgev2.Portal, meta *PortalMetadata) string {
	portalRoomID := ""
	if portal != nil && portal.MXID != "" {
		portalRoomID = portal.MXID.String()
	}
	return integrationSessionKind(string(current), portalRoomID, meta)
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
