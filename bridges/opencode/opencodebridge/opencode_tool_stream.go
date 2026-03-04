package opencodebridge

import (
	"context"
	"strings"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/ai-bridge/bridges/opencode/opencode"
)

func opencodeToolCallID(part opencode.Part) string {
	callID := strings.TrimSpace(part.CallID)
	if callID == "" {
		callID = part.ID
	}
	return callID
}

func opencodeToolName(part opencode.Part) string {
	toolName := strings.TrimSpace(part.Tool)
	if toolName == "" {
		toolName = "tool"
	}
	return toolName
}

func (m *OpenCodeManager) emitToolStreamDelta(ctx context.Context, inst *openCodeInstance, portal *bridgev2.Portal, part opencode.Part, delta string) {
	if m == nil || m.bridge == nil || portal == nil {
		return
	}
	if delta == "" {
		return
	}
	turnID := partTurnID(part)
	toolCallID := opencodeToolCallID(part)
	if toolCallID == "" {
		return
	}
	toolName := opencodeToolName(part)
	agentID := m.bridge.portalAgentID(portal)
	m.ensureStepStarted(ctx, inst, portal, part.SessionID, part.MessageID)
	sf := inst.partStreamFlags(part.SessionID, part.ID)
	if !sf.inputStarted {
		m.bridge.emitOpenCodeStreamEvent(ctx, portal, turnID, agentID, map[string]any{
			"type":             "tool-input-start",
			"toolCallId":       toolCallID,
			"toolName":         toolName,
			"title":            toolDisplayTitle(toolName),
			"providerExecuted": false,
		})
		inst.setPartStreamInputStarted(part.SessionID, part.ID)
	}
	m.bridge.emitOpenCodeStreamEvent(ctx, portal, turnID, agentID, map[string]any{
		"type":           "tool-input-delta",
		"toolCallId":     toolCallID,
		"inputTextDelta": delta,
	})
}

func (m *OpenCodeManager) emitToolStreamState(ctx context.Context, inst *openCodeInstance, portal *bridgev2.Portal, part opencode.Part, _ string) {
	if m == nil || m.bridge == nil || portal == nil || part.State == nil {
		return
	}
	turnID := partTurnID(part)
	toolCallID := opencodeToolCallID(part)
	if toolCallID == "" {
		return
	}
	toolName := opencodeToolName(part)
	agentID := m.bridge.portalAgentID(portal)
	m.ensureStepStarted(ctx, inst, portal, part.SessionID, part.MessageID)
	sf := inst.partStreamFlags(part.SessionID, part.ID)

	if len(part.State.Input) > 0 && !sf.inputAvailable {
		if !sf.inputStarted {
			m.bridge.emitOpenCodeStreamEvent(ctx, portal, turnID, agentID, map[string]any{
				"type":             "tool-input-start",
				"toolCallId":       toolCallID,
				"toolName":         toolName,
				"title":            toolDisplayTitle(toolName),
				"providerExecuted": false,
			})
			inst.setPartStreamInputStarted(part.SessionID, part.ID)
		}
		m.bridge.emitOpenCodeStreamEvent(ctx, portal, turnID, agentID, map[string]any{
			"type":             "tool-input-available",
			"toolCallId":       toolCallID,
			"toolName":         toolName,
			"input":            part.State.Input,
			"providerExecuted": false,
		})
		inst.setPartStreamInputAvailable(part.SessionID, part.ID)
	}

	if part.State.Output != "" && !sf.outputAvailable {
		m.bridge.emitOpenCodeStreamEvent(ctx, portal, turnID, agentID, map[string]any{
			"type":             "tool-output-available",
			"toolCallId":       toolCallID,
			"output":           part.State.Output,
			"providerExecuted": false,
		})
		inst.setPartStreamOutputAvailable(part.SessionID, part.ID)
	}

	if part.State.Error != "" && !sf.outputError {
		m.bridge.emitOpenCodeStreamEvent(ctx, portal, turnID, agentID, map[string]any{
			"type":             "tool-output-error",
			"toolCallId":       toolCallID,
			"errorText":        part.State.Error,
			"providerExecuted": false,
		})
		inst.setPartStreamOutputError(part.SessionID, part.ID)
	}
}
