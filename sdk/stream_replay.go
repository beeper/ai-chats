package sdk

import (
	"strings"

	"github.com/beeper/agentremote/pkg/shared/citations"
	"github.com/beeper/agentremote/pkg/shared/streamui"
)

type UIStateReplayer struct {
	state *streamui.UIState
}

func NewUIStateReplayer(state *streamui.UIState) UIStateReplayer {
	if state != nil {
		state.InitMaps()
	}
	return UIStateReplayer{state: state}
}

func (r UIStateReplayer) valid() bool {
	return r.state != nil
}

func (r UIStateReplayer) apply(part map[string]any) {
	if !r.valid() || len(part) == 0 {
		return
	}
	streamui.ApplyChunk(r.state, part)
}

func (r UIStateReplayer) Start(metadata map[string]any) {
	if !r.valid() {
		return
	}
	part := map[string]any{
		"type":      "start",
		"messageId": r.state.TurnID,
	}
	if len(metadata) > 0 {
		part["messageMetadata"] = metadata
	}
	r.apply(part)
}

func (r UIStateReplayer) Finish(finishReason string, metadata map[string]any) {
	if !r.valid() {
		return
	}
	finishReason = strings.TrimSpace(finishReason)
	if finishReason == "" {
		finishReason = "stop"
	}
	part := map[string]any{
		"type":         "finish",
		"finishReason": finishReason,
	}
	if len(metadata) > 0 {
		part["messageMetadata"] = metadata
	}
	r.apply(part)
}

func (r UIStateReplayer) StepStart() {
	r.apply(map[string]any{"type": "start-step"})
}

func (r UIStateReplayer) StepFinish() {
	r.apply(map[string]any{"type": "finish-step"})
}

func (r UIStateReplayer) Text(partID, text string) {
	partID = strings.TrimSpace(partID)
	text = strings.TrimSpace(text)
	if partID == "" || text == "" {
		return
	}
	r.apply(map[string]any{"type": "text-start", "id": partID})
	r.apply(map[string]any{"type": "text-delta", "id": partID, "delta": text})
	r.apply(map[string]any{"type": "text-end", "id": partID})
}

func (r UIStateReplayer) Reasoning(partID, text string) {
	partID = strings.TrimSpace(partID)
	text = strings.TrimSpace(text)
	if partID == "" || text == "" {
		return
	}
	r.apply(map[string]any{"type": "reasoning-start", "id": partID})
	r.apply(map[string]any{"type": "reasoning-delta", "id": partID, "delta": text})
	r.apply(map[string]any{"type": "reasoning-end", "id": partID})
}

func (r UIStateReplayer) ToolInput(toolCallID, toolName string, input any, providerExecuted bool) {
	toolCallID = strings.TrimSpace(toolCallID)
	if toolCallID == "" {
		return
	}
	r.apply(map[string]any{
		"type":             "tool-input-available",
		"toolCallId":       toolCallID,
		"toolName":         strings.TrimSpace(toolName),
		"input":            input,
		"providerExecuted": providerExecuted,
	})
}

func (r UIStateReplayer) ToolInputText(toolCallID, toolName, inputText string, providerExecuted bool) {
	toolCallID = strings.TrimSpace(toolCallID)
	inputText = strings.TrimSpace(inputText)
	if toolCallID == "" || inputText == "" {
		return
	}
	r.apply(map[string]any{
		"type":             "tool-input-start",
		"toolCallId":       toolCallID,
		"toolName":         strings.TrimSpace(toolName),
		"providerExecuted": providerExecuted,
	})
	r.apply(map[string]any{
		"type":             "tool-input-delta",
		"toolCallId":       toolCallID,
		"inputTextDelta":   inputText,
		"providerExecuted": providerExecuted,
	})
}

func (r UIStateReplayer) ToolOutput(toolCallID string, output any, providerExecuted bool) {
	toolCallID = strings.TrimSpace(toolCallID)
	if toolCallID == "" {
		return
	}
	r.apply(map[string]any{
		"type":             "tool-output-available",
		"toolCallId":       toolCallID,
		"output":           output,
		"providerExecuted": providerExecuted,
	})
}

func (r UIStateReplayer) ToolOutputError(toolCallID, errorText string, providerExecuted bool) {
	toolCallID = strings.TrimSpace(toolCallID)
	if toolCallID == "" {
		return
	}
	r.apply(map[string]any{
		"type":             "tool-output-error",
		"toolCallId":       toolCallID,
		"errorText":        strings.TrimSpace(errorText),
		"providerExecuted": providerExecuted,
	})
}

func (r UIStateReplayer) ToolOutputDenied(toolCallID string) {
	toolCallID = strings.TrimSpace(toolCallID)
	if toolCallID == "" {
		return
	}
	r.apply(map[string]any{
		"type":       "tool-output-denied",
		"toolCallId": toolCallID,
	})
}

func (r UIStateReplayer) ApprovalRequest(approvalID, toolCallID string) {
	approvalID = strings.TrimSpace(approvalID)
	toolCallID = strings.TrimSpace(toolCallID)
	if approvalID == "" || toolCallID == "" {
		return
	}
	r.apply(map[string]any{
		"type":       "tool-approval-request",
		"approvalId": approvalID,
		"toolCallId": toolCallID,
	})
}

func (r UIStateReplayer) File(url, mediaType, filename string) {
	url = strings.TrimSpace(url)
	if url == "" {
		return
	}
	part := map[string]any{
		"type":      "file",
		"url":       url,
		"mediaType": strings.TrimSpace(mediaType),
	}
	if part["mediaType"] == "" {
		part["mediaType"] = "application/octet-stream"
	}
	if trimmed := strings.TrimSpace(filename); trimmed != "" {
		part["filename"] = trimmed
	}
	r.apply(part)
}

func (r UIStateReplayer) SourceURL(citation citations.SourceCitation, sourceID string) {
	if strings.TrimSpace(citation.URL) == "" {
		return
	}
	part := map[string]any{
		"type": "source-url",
		"url":  strings.TrimSpace(citation.URL),
	}
	if trimmed := strings.TrimSpace(citation.Title); trimmed != "" {
		part["title"] = trimmed
	}
	if trimmed := strings.TrimSpace(sourceID); trimmed != "" {
		part["sourceId"] = trimmed
	}
	r.apply(part)
}

func (r UIStateReplayer) SourceDocument(doc citations.SourceDocument) {
	sourceID := strings.TrimSpace(doc.ID)
	title := strings.TrimSpace(doc.Title)
	filename := strings.TrimSpace(doc.Filename)
	if sourceID == "" && title == "" && filename == "" {
		return
	}
	part := map[string]any{
		"type": "source-document",
	}
	if sourceID != "" {
		part["sourceId"] = sourceID
	}
	if title != "" {
		part["title"] = title
	}
	if filename != "" {
		part["filename"] = filename
	}
	if mediaType := strings.TrimSpace(doc.MediaType); mediaType != "" {
		part["mediaType"] = mediaType
	}
	r.apply(part)
}

func (r UIStateReplayer) Artifact(sourceID string, citation citations.SourceCitation, doc citations.SourceDocument, mediaType string) {
	if trimmed := strings.TrimSpace(citation.URL); trimmed != "" {
		r.File(trimmed, mediaType, doc.Filename)
		r.SourceURL(citations.SourceCitation{
			URL:   trimmed,
			Title: doc.Title,
		}, sourceID)
	}
	if strings.TrimSpace(doc.MediaType) == "" {
		doc.MediaType = strings.TrimSpace(mediaType)
	}
	r.SourceDocument(doc)
}

func (r UIStateReplayer) DataPart(part map[string]any) {
	r.apply(part)
}
