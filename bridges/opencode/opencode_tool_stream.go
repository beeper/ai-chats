package opencode

import (
	"context"
	"strings"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote/bridges/opencode/api"
	"github.com/beeper/agentremote/pkg/shared/citations"
	bridgesdk "github.com/beeper/agentremote/sdk"
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
		tools.EnsureInputStart(ctx, toolCallID, nil, bridgesdk.ToolInputOptions{
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
			tools.EnsureInputStart(ctx, toolCallID, nil, bridgesdk.ToolInputOptions{
				ToolName:         toolName,
				ProviderExecuted: false,
			})
			inst.withPartState(part.SessionID, part.ID, func(ps *openCodePartState) { ps.streamInputStarted = true })
		}
		tools.Input(ctx, toolCallID, toolName, part.State.Input, false)
		inst.withPartState(part.SessionID, part.ID, func(ps *openCodePartState) { ps.streamInputAvailable = true })
	}

	if part.State.Output != "" && !sf.outputAvailable {
		tools.Output(ctx, toolCallID, part.State.Output, bridgesdk.ToolOutputOptions{ProviderExecuted: false})
		inst.withPartState(part.SessionID, part.ID, func(ps *openCodePartState) { ps.streamOutputAvailable = true })
	}

	if part.State.Error != "" && !sf.outputError {
		tools.OutputError(ctx, toolCallID, part.State.Error, false)
		inst.withPartState(part.SessionID, part.ID, func(ps *openCodePartState) { ps.streamOutputError = true })
	}
}

func (m *OpenCodeManager) emitArtifactStream(ctx context.Context, inst *openCodeInstance, portal *bridgev2.Portal, part api.Part) {
	if m == nil || m.bridge == nil || portal == nil || inst == nil {
		return
	}
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
