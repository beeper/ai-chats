package opencode

import (
	"context"
	"strings"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote/bridges/opencode/api"
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

func opencodePartStreamID(part api.Part, kind string) string {
	if part.ID == "" {
		return ""
	}
	if kind == "reasoning" {
		return "reasoning-" + part.ID
	}
	return "text-" + part.ID
}

// partTurnID returns the stream turn ID for a part, falling back to the part ID.
func partTurnID(part api.Part) string {
	turnID := opencodeMessageStreamTurnID(part.SessionID, part.MessageID)
	if turnID == "" {
		return "opencode-part-" + part.ID
	}
	return turnID
}

func (m *OpenCodeManager) emitTextStreamDeltaForKind(ctx context.Context, inst *openCodeInstance, portal *bridgev2.Portal, part api.Part, delta, kind string) {
	if m == nil || m.bridge == nil || portal == nil || inst == nil || delta == "" {
		return
	}
	partID := opencodePartStreamID(part, kind)
	if partID == "" {
		return
	}
	m.closeStepIfOpen(ctx, inst, portal, part.SessionID, part.MessageID)

	started, _ := inst.partTextStreamFlags(part.SessionID, part.ID).forKind(kind)
	turnID := partTurnID(part)
	agentID := m.bridge.portalAgentID(portal)
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
	inst.appendPartTextContent(part.SessionID, part.ID, kind, delta)
}

func (m *OpenCodeManager) emitTextStreamEnd(ctx context.Context, inst *openCodeInstance, portal *bridgev2.Portal, part api.Part) {
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
	partID := opencodePartStreamID(part, kind)
	if partID == "" {
		return
	}
	started, ended := inst.partTextStreamFlags(part.SessionID, part.ID).forKind(kind)
	if !started || ended {
		return
	}
	m.bridge.emitOpenCodeStreamEvent(ctx, portal, partTurnID(part), m.bridge.portalAgentID(portal), map[string]any{
		"type": kind + "-end",
		"id":   partID,
	})
	inst.setPartTextStreamEnded(part.SessionID, part.ID, kind)
}
