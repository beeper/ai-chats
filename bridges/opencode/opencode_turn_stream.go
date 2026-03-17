package opencode

import (
	"context"

	"maunium.net/go/mautrix/bridgev2"

	bridgesdk "github.com/beeper/agentremote/sdk"
)

func (m *OpenCodeManager) ensureTurnStarted(ctx context.Context, inst *openCodeInstance, portal *bridgev2.Portal, sessionID, messageID string, metadata map[string]any) {
	if m == nil || m.bridge == nil || inst == nil || portal == nil {
		return
	}
	if sessionID == "" || messageID == "" {
		return
	}
	state := inst.ensureTurnState(sessionID, messageID)
	if state == nil {
		return
	}
	if state.started {
		if len(metadata) > 0 {
			m.applyTurnMetadata(ctx, portal, sessionID, messageID, metadata)
		}
		return
	}
	streamState, writer := m.mustStreamWriter(ctx, portal, sessionID, messageID)
	if len(metadata) > 0 {
		m.bridge.host.applyStreamMessageMetadata(streamState, metadata)
		writer.MessageMetadata(ctx, metadata)
	} else {
		writer.MessageMetadata(ctx, nil)
	}
	state.started = true
}

func (m *OpenCodeManager) ensureStepStarted(ctx context.Context, inst *openCodeInstance, portal *bridgev2.Portal, sessionID, messageID string) {
	if m == nil || m.bridge == nil || inst == nil || portal == nil {
		return
	}
	if sessionID == "" || messageID == "" {
		return
	}
	m.ensureTurnStarted(ctx, inst, portal, sessionID, messageID, nil)
	state := inst.turnStateFor(sessionID, messageID)
	if state == nil || state.stepOpen {
		return
	}
	_, writer := m.mustStreamWriter(ctx, portal, sessionID, messageID)
	writer.StepStart(ctx)
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
	_, writer := m.mustStreamWriter(ctx, portal, sessionID, messageID)
	writer.StepFinish(ctx)
	state.stepOpen = false
}

func (m *OpenCodeManager) emitTurnFinish(ctx context.Context, inst *openCodeInstance, portal *bridgev2.Portal, sessionID, messageID, finishReason string, metadata map[string]any) {
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
	if len(metadata) > 0 {
		m.applyTurnMetadata(ctx, portal, sessionID, messageID, metadata)
	}
	m.bridge.emitOpenCodeStreamEvent(ctx, portal, turnID, m.bridge.portalAgentID(portal), map[string]any{
		"type":            "finish",
		"finishReason":    finishReason,
		"messageMetadata": metadata,
	})
	m.bridge.finishOpenCodeStream(turnID)
	state.finished = true
	inst.removeTurnState(sessionID, messageID)
}

func (m *OpenCodeManager) applyTurnMetadata(ctx context.Context, portal *bridgev2.Portal, sessionID, messageID string, metadata map[string]any) {
	state, writer := m.mustStreamWriter(ctx, portal, sessionID, messageID)
	if len(metadata) > 0 {
		m.bridge.host.applyStreamMessageMetadata(state, metadata)
	}
	writer.MessageMetadata(ctx, metadata)
}

func (m *OpenCodeManager) mustStreamWriter(ctx context.Context, portal *bridgev2.Portal, sessionID, messageID string) (*openCodeStreamState, *bridgesdk.Writer) {
	turnID := opencodeMessageStreamTurnID(sessionID, messageID)
	state, writer := m.bridge.host.ensureStreamWriter(ctx, portal, turnID, m.bridge.portalAgentID(portal))
	return state, writer
}
