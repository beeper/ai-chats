package opencodebridge

import (
	"context"
	"strings"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/ai-bridge/bridges/opencode/opencode"
)

func opencodeMessageStreamTurnID(sessionID, messageID string) string {
	sessionID = strings.TrimSpace(sessionID)
	messageID = strings.TrimSpace(messageID)
	if sessionID != "" && messageID != "" {
		return "opencode-msg-" + sessionID + "-" + messageID
	}
	if messageID != "" {
		return "opencode-msg-" + messageID
	}
	return ""
}

func opencodePartStreamID(part opencode.Part, kind string) string {
	if part.ID == "" {
		return ""
	}
	if kind == "reasoning" {
		return "reasoning-" + part.ID
	}
	return "text-" + part.ID
}

func (m *OpenCodeManager) emitTextStreamDelta(ctx context.Context, inst *openCodeInstance, portal *bridgev2.Portal, part opencode.Part, delta string) {
	m.emitTextStreamDeltaForKind(ctx, inst, portal, part, delta, "text")
}

func (m *OpenCodeManager) emitReasoningStreamDelta(ctx context.Context, inst *openCodeInstance, portal *bridgev2.Portal, part opencode.Part, delta string) {
	m.emitTextStreamDeltaForKind(ctx, inst, portal, part, delta, "reasoning")
}

func (m *OpenCodeManager) emitTextStreamDeltaForKind(ctx context.Context, inst *openCodeInstance, portal *bridgev2.Portal, part opencode.Part, delta, kind string) {
	if m == nil || m.bridge == nil || portal == nil || inst == nil {
		return
	}
	if delta == "" {
		return
	}
	turnID := opencodeMessageStreamTurnID(part.SessionID, part.MessageID)
	if turnID == "" {
		turnID = "opencode-part-" + part.ID
	}
	partID := opencodePartStreamID(part, kind)
	if partID == "" {
		return
	}
	agentID := m.bridge.portalAgentID(portal)
	m.closeStepIfOpen(ctx, inst, portal, part.SessionID, part.MessageID)
	m.ensureTurnStarted(ctx, inst, portal, part.SessionID, part.MessageID)
	textStarted, _, reasoningStarted, _ := inst.partTextStreamFlags(part.SessionID, part.ID)
	started := textStarted
	if kind == "reasoning" {
		started = reasoningStarted
	}
	if !started {
		m.bridge.emitOpenCodeStreamEvent(ctx, portal, turnID, agentID, map[string]any{
			"type": kind + "-start",
			"id":   partID,
		})
		inst.setPartTextStreamStarted(part.SessionID, part.ID, kind)
	}
	m.bridge.emitOpenCodeStreamEvent(ctx, portal, turnID, agentID, map[string]any{
		"type":  kind + "-delta",
		"id":    partID,
		"delta": delta,
	})
}

func (m *OpenCodeManager) emitTextStreamEnd(ctx context.Context, inst *openCodeInstance, portal *bridgev2.Portal, part opencode.Part) {
	if m == nil || m.bridge == nil || portal == nil || inst == nil {
		return
	}
	if part.Time == nil || part.Time.End == 0 {
		return
	}
	if part.Type != "text" && part.Type != "reasoning" {
		return
	}
	kind := part.Type
	turnID := opencodeMessageStreamTurnID(part.SessionID, part.MessageID)
	if turnID == "" {
		turnID = "opencode-part-" + part.ID
	}
	partID := opencodePartStreamID(part, kind)
	if partID == "" {
		return
	}
	agentID := m.bridge.portalAgentID(portal)
	textStarted, textEnded, reasoningStarted, reasoningEnded := inst.partTextStreamFlags(part.SessionID, part.ID)
	started := textStarted
	ended := textEnded
	if kind == "reasoning" {
		started = reasoningStarted
		ended = reasoningEnded
	}
	if !started || ended {
		return
	}
	m.bridge.emitOpenCodeStreamEvent(ctx, portal, turnID, agentID, map[string]any{
		"type": kind + "-end",
		"id":   partID,
	})
	inst.setPartTextStreamEnded(part.SessionID, part.ID, kind)
}
