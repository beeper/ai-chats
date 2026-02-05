package connector

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

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
	sessionKey := tools.ReadStringDefault(args, "sessionKey", "")
	label := tools.ReadStringDefault(args, "label", "")
	agentID := tools.ReadStringDefault(args, "agentId", "")
	if sessionKey != "" && label != "" {
		return tools.JSONResult(map[string]any{
			"status": "error",
			"error":  "Provide either sessionKey or label (not both).",
		}), nil
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
	} else {
		if strings.TrimSpace(label) == "" {
			return tools.JSONResult(map[string]any{
				"status": "error",
				"error":  "sessionKey or label is required",
			}), nil
		}
		target, display, resolveErr := oc.resolveSessionPortalByLabel(ctx, label, agentID)
		if resolveErr != nil {
			return tools.JSONResult(map[string]any{
				"status": "error",
				"error":  resolveErr.Error(),
			}), nil
		}
		targetPortal = target
		displayKey = display
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
