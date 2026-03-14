package opencode

import (
	"context"
	"strings"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote/bridges/opencode/api"
	"github.com/beeper/agentremote/pkg/shared/streamui"
)

func opencodeToolCallID(part api.Part) string {
	callID := strings.TrimSpace(part.CallID)
	if callID == "" {
		callID = part.ID
	}
	return callID
}

func opencodeToolName(part api.Part) string {
	toolName := strings.TrimSpace(part.Tool)
	if toolName == "" {
		toolName = "tool"
	}
	return toolName
}

func (m *OpenCodeManager) emitToolStreamDelta(ctx context.Context, inst *openCodeInstance, portal *bridgev2.Portal, part api.Part, delta string) {
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
			"title":            streamui.ToolDisplayTitle(toolName),
			"providerExecuted": false,
		})
		inst.withPartState(part.SessionID, part.ID, func(ps *openCodePartState) { ps.streamInputStarted = true })
	}
	m.bridge.emitOpenCodeStreamEvent(ctx, portal, turnID, agentID, map[string]any{
		"type":           "tool-input-delta",
		"toolCallId":     toolCallID,
		"inputTextDelta": delta,
	})
}

func (m *OpenCodeManager) emitToolStreamState(ctx context.Context, inst *openCodeInstance, portal *bridgev2.Portal, part api.Part, _ string) {
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
				"title":            streamui.ToolDisplayTitle(toolName),
				"providerExecuted": false,
			})
			inst.withPartState(part.SessionID, part.ID, func(ps *openCodePartState) { ps.streamInputStarted = true })
		}
		m.bridge.emitOpenCodeStreamEvent(ctx, portal, turnID, agentID, map[string]any{
			"type":             "tool-input-available",
			"toolCallId":       toolCallID,
			"toolName":         toolName,
			"input":            part.State.Input,
			"providerExecuted": false,
		})
		inst.withPartState(part.SessionID, part.ID, func(ps *openCodePartState) { ps.streamInputAvailable = true })
	}

	if part.State.Output != "" && !sf.outputAvailable {
		m.bridge.emitOpenCodeStreamEvent(ctx, portal, turnID, agentID, map[string]any{
			"type":             "tool-output-available",
			"toolCallId":       toolCallID,
			"output":           part.State.Output,
			"providerExecuted": false,
		})
		inst.withPartState(part.SessionID, part.ID, func(ps *openCodePartState) { ps.streamOutputAvailable = true })
	}

	if part.State.Error != "" && !sf.outputError {
		m.bridge.emitOpenCodeStreamEvent(ctx, portal, turnID, agentID, map[string]any{
			"type":             "tool-output-error",
			"toolCallId":       toolCallID,
			"errorText":        part.State.Error,
			"providerExecuted": false,
		})
		inst.withPartState(part.SessionID, part.ID, func(ps *openCodePartState) { ps.streamOutputError = true })
	}
}

func (m *OpenCodeManager) emitArtifactStream(ctx context.Context, inst *openCodeInstance, portal *bridgev2.Portal, part api.Part) {
	if m == nil || m.bridge == nil || portal == nil || inst == nil {
		return
	}
	turnID := partTurnID(part)
	agentID := m.bridge.portalAgentID(portal)
	if state := inst.partState(part.SessionID, part.ID); state != nil && state.artifactStreamSent {
		return
	}
	sourceURL := strings.TrimSpace(part.URL)
	title := strings.TrimSpace(part.Filename)
	if title == "" {
		title = strings.TrimSpace(part.Name)
	}
	if sourceURL == "" && title == "" {
		return
	}

	mediaType := strings.TrimSpace(part.Mime)
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}

	if sourceURL != "" {
		m.bridge.emitOpenCodeStreamEvent(ctx, portal, turnID, agentID, map[string]any{
			"type":      "file",
			"url":       sourceURL,
			"mediaType": mediaType,
		})
	}

	if title != "" {
		filename := strings.TrimSpace(part.Filename)
		if filename == "" {
			filename = title
		}
		m.bridge.emitOpenCodeStreamEvent(ctx, portal, turnID, agentID, map[string]any{
			"type":      "source-document",
			"sourceId":  "opencode-doc-" + part.ID,
			"title":     title,
			"filename":  filename,
			"mediaType": mediaType,
		})
	}

	if sourceURL != "" {
		m.bridge.emitOpenCodeStreamEvent(ctx, portal, turnID, agentID, map[string]any{
			"type":     "source-url",
			"sourceId": "opencode-source-" + part.ID,
			"url":      sourceURL,
			"title":    title,
		})
	}

	inst.markPartArtifactStreamSent(part.SessionID, part.ID)
}
