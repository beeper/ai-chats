package opencode

import (
	"context"
	"slices"
	"strings"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote/bridges/opencode/api"
)

func (m *OpenCodeManager) syncAssistantMessagePart(ctx context.Context, inst *openCodeInstance, portal *bridgev2.Portal, msg *api.MessageWithParts, part api.Part) {
	if m == nil || inst == nil || portal == nil || msg == nil {
		return
	}
	completed := msg.Info.Time.Completed != 0
	switch part.Type {
	case "text", "reasoning":
		m.syncAssistantTextPart(ctx, inst, portal, part, completed)
	case "tool":
		m.handleToolPart(ctx, inst, portal, "assistant", part)
	case "file":
		inst.ensurePartState(part.SessionID, part.MessageID, part.ID, "assistant", part.Type)
		m.emitArtifactStream(ctx, inst, portal, part)
	case "step-start":
		m.ensureStepStarted(ctx, inst, portal, part.SessionID, part.MessageID)
	case "step-finish":
		m.closeStepIfOpen(ctx, inst, portal, part.SessionID, part.MessageID)
		m.emitDataPartStream(ctx, inst, portal, part)
	case "patch", "snapshot", "agent", "subtask", "retry", "compaction":
		inst.ensurePartState(part.SessionID, part.MessageID, part.ID, "assistant", part.Type)
		m.emitDataPartStream(ctx, inst, portal, part)
	}
}

func (m *OpenCodeManager) syncAssistantTextPart(ctx context.Context, inst *openCodeInstance, portal *bridgev2.Portal, part api.Part, completed bool) {
	if m == nil || inst == nil || portal == nil {
		return
	}
	text := part.Text
	if text == "" && !(completed || (part.Time != nil && part.Time.End > 0)) {
		return
	}
	kind := part.Type
	partID := opencodePartStreamID(part, kind)
	if partID == "" {
		return
	}
	flags := inst.partTextStreamFlags(part.SessionID, part.ID)
	delivered := inst.partTextContent(part.SessionID, part.ID, kind)
	started, ended := flags.forKind(kind)
	turnID := partTurnID(part)
	agentID := m.bridge.portalAgentID(portal)
	m.closeStepIfOpen(ctx, inst, portal, part.SessionID, part.MessageID)
	if !started {
		m.bridge.emitOpenCodeStreamEvent(ctx, portal, turnID, agentID, map[string]any{
			"type": kind + "-start",
			"id":   partID,
		})
		inst.setPartTextStreamStarted(part.SessionID, part.ID, kind)
		if text != "" {
			m.bridge.emitOpenCodeStreamEvent(ctx, portal, turnID, agentID, map[string]any{
				"type":  kind + "-delta",
				"id":    partID,
				"delta": text,
			})
			inst.appendPartTextContent(part.SessionID, part.ID, kind, text)
			delivered = text
		}
	} else if missing, ok := strings.CutPrefix(text, delivered); ok && missing != "" {
		m.bridge.emitOpenCodeStreamEvent(ctx, portal, turnID, agentID, map[string]any{
			"type":  kind + "-delta",
			"id":    partID,
			"delta": missing,
		})
		inst.appendPartTextContent(part.SessionID, part.ID, kind, missing)
	}
	if ended {
		return
	}
	if completed || (part.Time != nil && part.Time.End > 0) {
		m.bridge.emitOpenCodeStreamEvent(ctx, portal, turnID, agentID, map[string]any{
			"type": kind + "-end",
			"id":   partID,
		})
		inst.setPartTextStreamEnded(part.SessionID, part.ID, kind)
	}
}

func (m *OpenCodeManager) emitDataPartStream(ctx context.Context, inst *openCodeInstance, portal *bridgev2.Portal, part api.Part) {
	if m == nil || inst == nil || portal == nil || part.ID == "" {
		return
	}
	if state := inst.partState(part.SessionID, part.ID); state != nil && state.dataStreamSent {
		return
	}
	data := BuildDataPartMap(part)
	if data == nil {
		return
	}
	turnID := partTurnID(part)
	m.bridge.emitOpenCodeStreamEvent(ctx, portal, turnID, m.bridge.portalAgentID(portal), data)
	inst.markPartDataStreamSent(part.SessionID, part.ID)
}

// BuildDataPartMap builds a map representation of an opencode data part for streaming or backfill.
// Returns nil for unknown part types.
func BuildDataPartMap(part api.Part) map[string]any {
	data := map[string]any{
		"type": "data-opencode-" + strings.TrimSpace(part.Type),
		"id":   part.ID,
	}
	switch part.Type {
	case "step-finish":
		if reason := strings.TrimSpace(part.Reason); reason != "" {
			data["reason"] = reason
		}
		if part.Cost != 0 {
			data["cost"] = part.Cost
		}
	case "patch":
		if hash := strings.TrimSpace(part.Hash); hash != "" {
			data["hash"] = hash
		}
		if len(part.Files) > 0 {
			data["files"] = slices.Clone(part.Files)
		}
	case "snapshot":
		if snapshot := strings.TrimSpace(part.Snapshot); snapshot != "" {
			data["snapshot"] = snapshot
		}
	case "agent":
		if name := strings.TrimSpace(part.Name); name != "" {
			data["name"] = name
		}
	case "subtask":
		if desc := strings.TrimSpace(part.Description); desc != "" {
			data["description"] = desc
		}
		if prompt := strings.TrimSpace(part.Prompt); prompt != "" {
			data["prompt"] = prompt
		}
		if agent := strings.TrimSpace(part.Agent); agent != "" {
			data["agent"] = agent
		}
	case "retry":
		if part.Attempt != 0 {
			data["attempt"] = part.Attempt
		}
		if len(part.Error) > 0 {
			data["error"] = string(part.Error)
		}
	case "compaction":
		data["auto"] = part.Auto
	default:
		return nil
	}
	return data
}
