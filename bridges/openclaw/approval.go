package openclaw

import (
	"context"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote/sdk"
)

func openClawApprovalDecisionStatus(decision string) (bool, string) {
	switch strings.ToLower(strings.TrimSpace(decision)) {
	case "allow-once":
		return true, "allow-once"
	case "allow-always":
		return true, "allow-always"
	case "deny":
		return false, "deny"
	default:
		return false, strings.TrimSpace(decision)
	}
}

func openClawApprovalPresentation(request map[string]any, command string) sdk.ApprovalPromptPresentation {
	command = strings.TrimSpace(command)
	details := make([]sdk.ApprovalDetail, 0, 5)
	if command != "" {
		details = append(details, sdk.ApprovalDetail{Label: "Command", Value: command})
	}
	if cwd := sdk.ValueSummary(request["cwd"]); cwd != "" {
		details = append(details, sdk.ApprovalDetail{Label: "Working directory", Value: cwd})
	}
	if reason := sdk.ValueSummary(request["reason"]); reason != "" {
		details = append(details, sdk.ApprovalDetail{Label: "Reason", Value: reason})
	}
	if sessionKey := sdk.ValueSummary(request["sessionKey"]); sessionKey != "" {
		details = append(details, sdk.ApprovalDetail{Label: "Session", Value: sessionKey})
	}
	if agent := sdk.ValueSummary(request["agentId"]); agent != "" {
		details = append(details, sdk.ApprovalDetail{Label: "Agent", Value: agent})
	}
	return sdk.BuildApprovalPresentation("OpenClaw execution request", command, details, true)
}

func openClawApprovalResolvedText(decision string) string {
	switch strings.ToLower(strings.TrimSpace(decision)) {
	case "allow-always":
		return "Tool approval allowed always"
	case "deny":
		return "Tool approval denied"
	default:
		return "Tool approval allowed"
	}
}

func mergeOpenClawApprovalData(dst *openClawPendingApprovalData, src openClawPendingApprovalData) {
	if dst == nil {
		return
	}
	if strings.TrimSpace(src.SessionKey) != "" {
		dst.SessionKey = strings.TrimSpace(src.SessionKey)
	}
	if strings.TrimSpace(src.AgentID) != "" {
		dst.AgentID = strings.TrimSpace(src.AgentID)
	}
	if strings.TrimSpace(src.TurnID) != "" {
		dst.TurnID = strings.TrimSpace(src.TurnID)
	}
	if strings.TrimSpace(src.ToolCallID) != "" {
		dst.ToolCallID = strings.TrimSpace(src.ToolCallID)
	}
	if strings.TrimSpace(src.ToolName) != "" {
		dst.ToolName = strings.TrimSpace(src.ToolName)
	}
	if strings.TrimSpace(src.Command) != "" {
		dst.Command = strings.TrimSpace(src.Command)
	}
	if strings.TrimSpace(src.Presentation.Title) != "" {
		dst.Presentation = src.Presentation
	}
	if src.CreatedAtMs != 0 {
		dst.CreatedAtMs = src.CreatedAtMs
	}
	if src.ExpiresAtMs != 0 {
		dst.ExpiresAtMs = src.ExpiresAtMs
	}
}

func (m *openClawManager) approvalHint(approvalID string) openClawPendingApprovalData {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.approvalHints[strings.TrimSpace(approvalID)]
}

func (m *openClawManager) setApprovalHint(approvalID string, update func(*openClawPendingApprovalData)) {
	approvalID = strings.TrimSpace(approvalID)
	if approvalID == "" || update == nil {
		return
	}
	m.mu.Lock()
	hint := m.approvalHints[approvalID]
	update(&hint)
	m.approvalHints[approvalID] = hint
	m.mu.Unlock()
}

func (m *openClawManager) clearApprovalHint(approvalID string) {
	approvalID = strings.TrimSpace(approvalID)
	if approvalID == "" {
		return
	}
	m.mu.Lock()
	delete(m.approvalHints, approvalID)
	m.mu.Unlock()
}

func (m *openClawManager) sendApprovalPrompt(ctx context.Context, portal *bridgev2.Portal, approvalID string, data *openClawPendingApprovalData) {
	if portal == nil || portal.MXID == "" || data == nil {
		return
	}
	toolCallID := strings.TrimSpace(data.ToolCallID)
	if toolCallID == "" {
		toolCallID = strings.TrimSpace(approvalID)
	}
	toolName := strings.TrimSpace(data.ToolName)
	if toolName == "" {
		toolName = "exec"
	}
	presentation := data.Presentation
	if strings.TrimSpace(presentation.Title) == "" {
		presentation = openClawApprovalPresentation(map[string]any{
			"sessionKey": data.SessionKey,
			"agentId":    data.AgentID,
		}, data.Command)
	}
	m.approvalFlow.SendPrompt(ctx, portal, sdk.SendPromptParams{
		ApprovalPromptMessageParams: sdk.ApprovalPromptMessageParams{
			ApprovalID:   approvalID,
			ToolCallID:   toolCallID,
			ToolName:     toolName,
			TurnID:       strings.TrimSpace(data.TurnID),
			Presentation: presentation,
			ExpiresAt:    time.UnixMilli(data.ExpiresAtMs),
		},
		RoomID:    portal.MXID,
		OwnerMXID: m.client.UserLogin.UserMXID,
	})
}

func (m *openClawManager) sendApprovalPromptWhenReady(ctx context.Context, portal *bridgev2.Portal, approvalID string) {
	deadline := time.Now().Add(350 * time.Millisecond)
	for {
		pending := m.approvalFlow.Get(approvalID)
		if pending == nil || pending.Data == nil {
			return
		}
		data := pending.Data
		if strings.TrimSpace(data.ToolCallID) != "" || strings.TrimSpace(data.TurnID) != "" || time.Now().After(deadline) {
			m.sendApprovalPrompt(ctx, portal, approvalID, data)
			return
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (m *openClawManager) handleApprovalRequest(ctx context.Context, payload gatewayApprovalRequestEvent) {
	hint := m.approvalHint(payload.ID)
	sessionKey := strings.TrimSpace(stringValue(payload.Request["sessionKey"]))
	if sessionKey == "" {
		sessionKey = strings.TrimSpace(hint.SessionKey)
	}
	if sessionKey == "" {
		return
	}
	portal := m.resolvePortal(ctx, sessionKey)
	if portal == nil || portal.MXID == "" {
		return
	}
	state, err := loadOpenClawPortalState(ctx, portal, m.client.UserLogin)
	if err != nil {
		return
	}
	agentID := resolveOpenClawAgentID(state, sessionKey, payload.Request)
	if strings.TrimSpace(hint.AgentID) != "" {
		agentID = strings.TrimSpace(hint.AgentID)
	}
	command := strings.TrimSpace(stringValue(payload.Request["command"]))
	presentation := openClawApprovalPresentation(payload.Request, command)
	data := &openClawPendingApprovalData{
		SessionKey:   sessionKey,
		AgentID:      agentID,
		Command:      command,
		Presentation: presentation,
		CreatedAtMs:  payload.CreatedAtMs,
		ExpiresAtMs:  payload.ExpiresAtMs,
	}
	mergeOpenClawApprovalData(data, hint)
	pending, created := m.approvalFlow.Register(payload.ID, time.Until(time.UnixMilli(payload.ExpiresAtMs)), data)
	if pending != nil && pending.Data != nil {
		mergeOpenClawApprovalData(pending.Data, hint)
		data = pending.Data
	}
	m.setApprovalHint(payload.ID, func(existing *openClawPendingApprovalData) {
		mergeOpenClawApprovalData(existing, *data)
	})
	if !created {
		return
	}
	go m.sendApprovalPromptWhenReady(m.client.BackgroundContext(ctx), portal, payload.ID)
}

func (m *openClawManager) handleApprovalResolved(ctx context.Context, payload gatewayApprovalResolvedEvent) {
	approvalID := strings.TrimSpace(payload.ID)
	if approvalID == "" {
		return
	}
	pending := m.approvalFlow.Get(approvalID)
	var data *openClawPendingApprovalData
	if pending != nil {
		data = pending.Data
	}
	sessionKey := strings.TrimSpace(stringValue(payload.Request["sessionKey"]))
	if sessionKey == "" && data != nil {
		sessionKey = strings.TrimSpace(data.SessionKey)
	}
	if sessionKey == "" {
		sessionKey = strings.TrimSpace(m.approvalHint(approvalID).SessionKey)
	}
	if sessionKey == "" {
		m.clearApprovalHint(approvalID)
		m.approvalFlow.Drop(approvalID)
		return
	}
	portal := m.resolvePortal(ctx, sessionKey)
	if portal == nil || portal.MXID == "" {
		m.clearApprovalHint(approvalID)
		m.approvalFlow.Drop(approvalID)
		return
	}
	state, err := loadOpenClawPortalState(ctx, portal, m.client.UserLogin)
	if err != nil {
		m.client.Log().Warn().Err(err).Str("portal_id", string(portal.PortalKey.ID)).Msg("Failed to load OpenClaw portal state for approval resolution")
		state = &openClawPortalState{}
	}
	approved, reason := openClawApprovalDecisionStatus(payload.Decision)
	resolvedBy := sdk.ApprovalResolutionOriginFromString(payload.ResolvedBy)
	if resolvedBy == "" {
		resolvedBy = sdk.ApprovalResolutionOriginAgent
	}
	if data != nil && strings.TrimSpace(data.TurnID) != "" && strings.TrimSpace(data.ToolCallID) != "" {
		m.client.EmitStreamPart(ctx, portal, data.TurnID, resolveOpenClawAgentID(state, sessionKey, payload.Request), sessionKey, map[string]any{
			"type":       "tool-approval-response",
			"approvalId": approvalID,
			"toolCallId": data.ToolCallID,
			"approved":   approved,
			"reason":     reason,
		})
	} else {
		m.client.sendSystemNotice(ctx, portal, m.approvalSenderForPortal(portal), openClawApprovalResolvedText(payload.Decision))
	}
	m.approvalFlow.ResolveExternal(ctx, approvalID, sdk.ApprovalDecisionPayload{
		ApprovalID: approvalID,
		Approved:   approved,
		Always:     strings.EqualFold(strings.TrimSpace(payload.Decision), "allow-always"),
		Reason:     reason,
		ResolvedBy: resolvedBy,
	})
	m.clearApprovalHint(approvalID)
}

func (m *openClawManager) attachApprovalContext(approvalID, sessionKey, agentID, turnID, toolCallID, toolName string) {
	approvalID = strings.TrimSpace(approvalID)
	if approvalID == "" {
		return
	}
	m.setApprovalHint(approvalID, func(hint *openClawPendingApprovalData) {
		mergeOpenClawApprovalData(hint, openClawPendingApprovalData{
			SessionKey: strings.TrimSpace(sessionKey),
			AgentID:    strings.TrimSpace(agentID),
			TurnID:     strings.TrimSpace(turnID),
			ToolCallID: strings.TrimSpace(toolCallID),
			ToolName:   strings.TrimSpace(toolName),
		})
	})
	m.approvalFlow.SetData(approvalID, func(pending *openClawPendingApprovalData) *openClawPendingApprovalData {
		if pending == nil {
			pending = &openClawPendingApprovalData{}
		}
		mergeOpenClawApprovalData(pending, openClawPendingApprovalData{
			SessionKey: strings.TrimSpace(sessionKey),
			AgentID:    strings.TrimSpace(agentID),
			TurnID:     strings.TrimSpace(turnID),
			ToolCallID: strings.TrimSpace(toolCallID),
			ToolName:   strings.TrimSpace(toolName),
		})
		return pending
	})
}
