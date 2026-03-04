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

// partTurnID returns the stream turn ID for a part, falling back to the part ID.
func partTurnID(part opencode.Part) string {
	turnID := opencodeMessageStreamTurnID(part.SessionID, part.MessageID)
	if turnID == "" {
		return "opencode-part-" + part.ID
	}
	return turnID
}

func (m *OpenCodeManager) emitTextStreamDelta(ctx context.Context, inst *openCodeInstance, portal *bridgev2.Portal, part opencode.Part, delta string) {
	m.emitTextStreamDeltaForKind(ctx, inst, portal, part, delta, "text")
}

func (m *OpenCodeManager) emitReasoningStreamDelta(ctx context.Context, inst *openCodeInstance, portal *bridgev2.Portal, part opencode.Part, delta string) {
	m.emitTextStreamDeltaForKind(ctx, inst, portal, part, delta, "reasoning")
}

func (m *OpenCodeManager) emitTextStreamDeltaForKind(ctx context.Context, inst *openCodeInstance, portal *bridgev2.Portal, part opencode.Part, delta, kind string) {
	if m == nil || m.bridge == nil || portal == nil || inst == nil || delta == "" {
		return
	}
	turnID := partTurnID(part)
	partID := opencodePartStreamID(part, kind)
	if partID == "" {
		return
	}
	agentID := m.bridge.portalAgentID(portal)
	m.closeStepIfOpen(ctx, inst, portal, part.SessionID, part.MessageID)
	m.ensureTurnStarted(ctx, inst, portal, part.SessionID, part.MessageID)

	tsf := inst.partTextStreamFlags(part.SessionID, part.ID)
	started := tsf.textStarted
	if kind == "reasoning" {
		started = tsf.reasoningStarted
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
	turnID := partTurnID(part)
	partID := opencodePartStreamID(part, kind)
	if partID == "" {
		return
	}
	agentID := m.bridge.portalAgentID(portal)
	tsf := inst.partTextStreamFlags(part.SessionID, part.ID)
	started := tsf.textStarted
	ended := tsf.textEnded
	if kind == "reasoning" {
		started = tsf.reasoningStarted
		ended = tsf.reasoningEnded
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
