package opencode

import (
	"context"
	"slices"
	"strings"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote/bridges/opencode/api"
	"github.com/beeper/agentremote/pkg/shared/citations"
	"github.com/beeper/agentremote/sdk"
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

func opencodeMessageStreamTurnID(sessionID, messageID string) string {
	sessionID = strings.TrimSpace(sessionID)
	messageID = strings.TrimSpace(messageID)
	if sessionID != "" && messageID != "" {
		return "opencode-msg-" + sessionID + "-" + messageID
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

func partTurnID(part api.Part) string {
	return opencodeMessageStreamTurnID(part.SessionID, part.MessageID)
}

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

func (m *OpenCodeManager) emitToolStreamDelta(ctx context.Context, inst *openCodeInstance, portal *bridgev2.Portal, part api.Part, delta string) {
	if m == nil || m.bridge == nil || portal == nil {
		return
	}
	if delta == "" {
		return
	}
	toolCallID := opencodeToolCallID(part)
	if toolCallID == "" {
		return
	}
	toolName := opencodeToolName(part)
	m.ensureStepStarted(ctx, inst, portal, part.SessionID, part.MessageID)
	sf := inst.partStreamFlags(part.SessionID, part.ID)
	_, writer := m.mustStreamWriter(ctx, portal, part.SessionID, part.MessageID)
	tools := writer.Tools()
	if !sf.inputStarted {
		tools.EnsureInputStart(ctx, toolCallID, nil, sdk.ToolInputOptions{
			ToolName:         toolName,
			ProviderExecuted: false,
		})
		inst.withPartState(part.SessionID, part.ID, func(ps *openCodePartState) { ps.streamInputStarted = true })
	}
	tools.InputDelta(ctx, toolCallID, toolName, delta, false)
}

func (m *OpenCodeManager) emitToolStreamState(ctx context.Context, inst *openCodeInstance, portal *bridgev2.Portal, part api.Part) {
	if m == nil || m.bridge == nil || portal == nil || part.State == nil {
		return
	}
	toolCallID := opencodeToolCallID(part)
	if toolCallID == "" {
		return
	}
	toolName := opencodeToolName(part)
	m.ensureStepStarted(ctx, inst, portal, part.SessionID, part.MessageID)
	sf := inst.partStreamFlags(part.SessionID, part.ID)
	_, writer := m.mustStreamWriter(ctx, portal, part.SessionID, part.MessageID)
	tools := writer.Tools()

	if len(part.State.Input) > 0 && !sf.inputAvailable {
		if !sf.inputStarted {
			tools.EnsureInputStart(ctx, toolCallID, nil, sdk.ToolInputOptions{
				ToolName:         toolName,
				ProviderExecuted: false,
			})
			inst.withPartState(part.SessionID, part.ID, func(ps *openCodePartState) { ps.streamInputStarted = true })
		}
		tools.Input(ctx, toolCallID, toolName, part.State.Input, false)
		inst.withPartState(part.SessionID, part.ID, func(ps *openCodePartState) { ps.streamInputAvailable = true })
	}

	if part.State.Output != "" && !sf.outputAvailable {
		tools.Output(ctx, toolCallID, part.State.Output, sdk.ToolOutputOptions{ProviderExecuted: false})
		inst.withPartState(part.SessionID, part.ID, func(ps *openCodePartState) { ps.streamOutputAvailable = true })
	}

	if part.State.Error != "" && !sf.outputError {
		tools.OutputError(ctx, toolCallID, part.State.Error, false)
		inst.withPartState(part.SessionID, part.ID, func(ps *openCodePartState) { ps.streamOutputError = true })
	}
}

func resolveArtifactFields(part api.Part) (sourceURL, title, mediaType string) {
	sourceURL = strings.TrimSpace(part.URL)
	title = strings.TrimSpace(part.Filename)
	if title == "" {
		title = strings.TrimSpace(part.Name)
	}
	mediaType = strings.TrimSpace(part.Mime)
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	return
}

func (m *OpenCodeManager) emitArtifactStream(ctx context.Context, inst *openCodeInstance, portal *bridgev2.Portal, part api.Part) {
	if m == nil || m.bridge == nil || portal == nil || inst == nil {
		return
	}
	if state := inst.partState(part.SessionID, part.ID); state != nil && state.artifactStreamSent {
		return
	}
	sourceURL, title, mediaType := resolveArtifactFields(part)
	if sourceURL == "" && title == "" {
		return
	}
	_, writer := m.mustStreamWriter(ctx, portal, part.SessionID, part.MessageID)

	if sourceURL != "" {
		writer.File(ctx, sourceURL, mediaType)
	}

	if title != "" {
		writer.SourceDocument(ctx, citations.SourceDocument{
			ID:        "opencode-doc-" + part.ID,
			Title:     title,
			Filename:  title,
			MediaType: mediaType,
		})
	}

	if sourceURL != "" {
		writer.SourceURL(ctx, citations.SourceCitation{
			URL:   sourceURL,
			Title: title,
		})
	}

	inst.markPartArtifactStreamSent(part.SessionID, part.ID)
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

func (m *OpenCodeManager) mustStreamWriter(ctx context.Context, portal *bridgev2.Portal, sessionID, messageID string) (*openCodeStreamState, *sdk.Writer) {
	turnID := opencodeMessageStreamTurnID(sessionID, messageID)
	state, writer := m.bridge.host.ensureStreamWriter(ctx, portal, turnID, m.bridge.portalAgentID(portal))
	return state, writer
}

func buildTurnStartMetadata(msg *api.MessageWithParts, agentID string) map[string]any {
	if msg == nil {
		return nil
	}
	metadata := map[string]any{
		"role":       strings.TrimSpace(msg.Info.Role),
		"session_id": strings.TrimSpace(msg.Info.SessionID),
		"message_id": strings.TrimSpace(msg.Info.ID),
		"agent_id":   strings.TrimSpace(agentID),
	}
	if msg.Info.ParentID != "" {
		metadata["parent_message_id"] = strings.TrimSpace(msg.Info.ParentID)
	}
	if msg.Info.Agent != "" {
		metadata["agent"] = strings.TrimSpace(msg.Info.Agent)
	}
	if msg.Info.ModelID != "" {
		metadata["model_id"] = strings.TrimSpace(msg.Info.ModelID)
	}
	if msg.Info.ProviderID != "" {
		metadata["provider_id"] = strings.TrimSpace(msg.Info.ProviderID)
	}
	if msg.Info.Mode != "" {
		metadata["mode"] = strings.TrimSpace(msg.Info.Mode)
	}
	if msg.Info.Time.Created > 0 {
		metadata["started_at"] = int64(msg.Info.Time.Created)
	}
	return metadata
}

func buildTurnFinishMetadata(msg *api.MessageWithParts, agentID, finishReason string) map[string]any {
	metadata := buildTurnStartMetadata(msg, agentID)
	if metadata == nil {
		metadata = map[string]any{"agent_id": strings.TrimSpace(agentID)}
	}
	if finishReason != "" {
		metadata["finish_reason"] = strings.TrimSpace(finishReason)
	} else if msg != nil && msg.Info.Finish != "" {
		metadata["finish_reason"] = strings.TrimSpace(msg.Info.Finish)
	}
	if msg != nil && msg.Info.Time.Completed > 0 {
		metadata["completed_at"] = int64(msg.Info.Time.Completed)
	}
	if msg != nil && msg.Info.Cost != 0 {
		metadata["cost"] = msg.Info.Cost
	}
	if msg != nil && msg.Info.Tokens != nil {
		applyTokenMetadata(metadata, msg.Info.Tokens)
	}
	if msg == nil {
		return metadata
	}
	for _, part := range msg.Parts {
		if part.Type != "step-finish" {
			continue
		}
		if part.Cost != 0 {
			metadata["cost"] = part.Cost
		}
		if part.Tokens != nil {
			applyTokenMetadata(metadata, part.Tokens)
		}
	}
	return metadata
}

func applyTokenMetadata(metadata map[string]any, tokens *api.TokenUsage) {
	metadata["prompt_tokens"] = int64(tokens.Input)
	metadata["completion_tokens"] = int64(tokens.Output)
	metadata["reasoning_tokens"] = int64(tokens.Reasoning)
	total := int64(tokens.Input + tokens.Output + tokens.Reasoning)
	if tokens.Cache != nil {
		total += int64(tokens.Cache.Read + tokens.Cache.Write)
	}
	metadata["total_tokens"] = total
}
