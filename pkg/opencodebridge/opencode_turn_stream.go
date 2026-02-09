package opencodebridge

import (
	"context"

	"maunium.net/go/mautrix/bridgev2"
)

func (m *OpenCodeManager) ensureTurnStarted(ctx context.Context, inst *openCodeInstance, portal *bridgev2.Portal, sessionID, messageID string) {
	if m == nil || m.bridge == nil || inst == nil || portal == nil {
		return
	}
	if sessionID == "" || messageID == "" {
		return
	}
	state := inst.ensureTurnState(sessionID, messageID)
	if state == nil || state.started {
		return
	}
	turnID := opencodeMessageStreamTurnID(sessionID, messageID)
	if turnID == "" {
		return
	}
	meta := m.bridge.portalMeta(portal)
	agentID := ""
	if meta != nil {
		agentID = meta.AgentID
	}
	m.bridge.emitOpenCodeStreamEvent(ctx, portal, turnID, agentID, map[string]any{
		"type":      "start",
		"messageId": turnID,
	})
	state.started = true
}

func (m *OpenCodeManager) ensureStepStarted(ctx context.Context, inst *openCodeInstance, portal *bridgev2.Portal, sessionID, messageID string) {
	if m == nil || m.bridge == nil || inst == nil || portal == nil {
		return
	}
	if sessionID == "" || messageID == "" {
		return
	}
	m.ensureTurnStarted(ctx, inst, portal, sessionID, messageID)
	state := inst.turnStateFor(sessionID, messageID)
	if state == nil || state.stepOpen {
		return
	}
	turnID := opencodeMessageStreamTurnID(sessionID, messageID)
	if turnID == "" {
		return
	}
	meta := m.bridge.portalMeta(portal)
	agentID := ""
	if meta != nil {
		agentID = meta.AgentID
	}
	m.bridge.emitOpenCodeStreamEvent(ctx, portal, turnID, agentID, map[string]any{
		"type": "start-step",
	})
	state.stepOpen = true
}

func (m *OpenCodeManager) closeStepIfOpen(ctx context.Context, inst *openCodeInstance, portal *bridgev2.Portal, sessionID, messageID string) {
	if m == nil || m.bridge == nil || inst == nil || portal == nil {
		return
	}
	if sessionID == "" || messageID == "" {
		return
	}
	state := inst.turnStateFor(sessionID, messageID)
	if state == nil || !state.stepOpen {
		return
	}
	turnID := opencodeMessageStreamTurnID(sessionID, messageID)
	if turnID == "" {
		return
	}
	meta := m.bridge.portalMeta(portal)
	agentID := ""
	if meta != nil {
		agentID = meta.AgentID
	}
	m.bridge.emitOpenCodeStreamEvent(ctx, portal, turnID, agentID, map[string]any{
		"type": "finish-step",
	})
	state.stepOpen = false
}

func (m *OpenCodeManager) emitTurnFinish(ctx context.Context, inst *openCodeInstance, portal *bridgev2.Portal, sessionID, messageID, finishReason string) {
	if m == nil || m.bridge == nil || inst == nil || portal == nil {
		return
	}
	if sessionID == "" || messageID == "" {
		return
	}
	state := inst.turnStateFor(sessionID, messageID)
	if state == nil || !state.started || state.finished {
		return
	}
	m.closeStepIfOpen(ctx, inst, portal, sessionID, messageID)
	turnID := opencodeMessageStreamTurnID(sessionID, messageID)
	if turnID == "" {
		return
	}
	if finishReason == "" {
		finishReason = "stop"
	}
	meta := m.bridge.portalMeta(portal)
	agentID := ""
	if meta != nil {
		agentID = meta.AgentID
	}
	m.bridge.emitOpenCodeStreamEvent(ctx, portal, turnID, agentID, map[string]any{
		"type":         "finish",
		"finishReason": finishReason,
	})
	m.bridge.finishOpenCodeStream(turnID)
	state.finished = true
	inst.removeTurnState(sessionID, messageID)
}
