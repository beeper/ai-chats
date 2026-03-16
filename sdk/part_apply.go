package sdk

import (
	"context"
	"strings"

	"github.com/beeper/agentremote/pkg/shared/citations"
)

// PartApplyOptions controls provider-specific edge cases when applying
// streamed UI/tool parts to a turn.
type PartApplyOptions struct {
	ResetMetadataOnStartMarkers     bool
	ResetMetadataOnEmptyMessageMeta bool
	ResetMetadataOnEmptyTextDelta   bool
	ResetMetadataOnAbort            bool
	ResetMetadataOnDataParts        bool
	HandleTerminalEvents            bool
	DefaultFinishReason             string
}

// ApplyStreamPart maps a canonical stream part onto a turn. It returns true when
// the part type is recognized and applied.
func ApplyStreamPart(turn *Turn, part map[string]any, opts PartApplyOptions) bool {
	if turn == nil || len(part) == 0 {
		return false
	}
	app := newPartApplicator(turn, part, opts)
	partType := app.s("type")
	if partType == "" {
		return false
	}
	switch partType {
	case "start", "message-metadata":
		app.messageMetadata()
	case "start-step":
		app.writer.StepStart(app.ctx)
	case "finish-step":
		app.writer.StepFinish(app.ctx)
	case "text-start", "reasoning-start":
		app.resetMetadataOn(app.opts.ResetMetadataOnStartMarkers)
	case "text-delta":
		app.textDelta()
	case "text-end":
		app.writer.FinishText(app.ctx)
	case "reasoning-delta":
		app.reasoningDelta()
	case "reasoning-end":
		app.writer.FinishReasoning(app.ctx)
	case "tool-input-start":
		app.tools.EnsureInputStart(app.ctx, app.s("toolCallId"), nil, ToolInputOptions{
			ToolName:         app.s("toolName"),
			ProviderExecuted: app.b("providerExecuted"),
		})
	case "tool-input-delta":
		app.tools.InputDelta(app.ctx, app.s("toolCallId"), "", app.s("inputTextDelta"), app.b("providerExecuted"))
	case "tool-input-available":
		app.tools.Input(app.ctx, app.s("toolCallId"), app.s("toolName"), app.part["input"], app.b("providerExecuted"))
	case "tool-output-available":
		app.tools.Output(app.ctx, app.s("toolCallId"), app.part["output"], ToolOutputOptions{
			ProviderExecuted: app.b("providerExecuted"),
		})
	case "tool-output-error":
		app.tools.OutputError(app.ctx, app.s("toolCallId"), app.s("errorText"), app.b("providerExecuted"))
	case "tool-output-denied":
		app.tools.Denied(app.ctx, app.s("toolCallId"))
	case "tool-approval-request":
		app.approvals.EmitRequest(app.ctx, app.s("approvalId"), app.s("toolCallId"))
	case "tool-approval-response":
		app.approvals.Respond(app.ctx, app.s("approvalId"), app.s("toolCallId"), app.b("approved"), app.s("reason"))
	case "file":
		app.writer.File(app.ctx, app.s("url"), app.s("mediaType"))
	case "source-document":
		app.writer.SourceDocument(app.ctx, app.sourceDocument())
	case "source-url":
		app.writer.SourceURL(app.ctx, app.sourceURL())
	case "error":
		app.writer.Error(app.ctx, app.s("errorText"))
	case "finish":
		if !app.opts.HandleTerminalEvents {
			return false
		}
		finishReason := app.s("finishReason")
		if finishReason == "" {
			finishReason = strings.TrimSpace(app.opts.DefaultFinishReason)
		}
		if finishReason == "" {
			finishReason = "stop"
		}
		app.turn.End(finishReason)
	case "abort":
		if !app.opts.HandleTerminalEvents {
			return false
		}
		app.resetMetadataOn(app.opts.ResetMetadataOnAbort)
		app.turn.Abort(app.s("reason"))
	default:
		if strings.HasPrefix(partType, "data-") {
			app.resetMetadataOn(app.opts.ResetMetadataOnDataParts)
			app.writer.RawPart(app.ctx, app.part)
			return true
		}
		return false
	}
	return true
}

type partApplicator struct {
	turn      *Turn
	part      map[string]any
	opts      PartApplyOptions
	ctx       context.Context
	writer    *Writer
	tools     *ToolsController
	approvals *ApprovalController
}

func newPartApplicator(turn *Turn, part map[string]any, opts PartApplyOptions) partApplicator {
	writer := turn.Writer()
	return partApplicator{
		turn:      turn,
		part:      part,
		opts:      opts,
		ctx:       turn.Context(),
		writer:    writer,
		tools:     writer.Tools(),
		approvals: turn.Approvals(),
	}
}

func (a partApplicator) s(key string) string {
	return strings.TrimSpace(stringValue(a.part[key]))
}

func (a partApplicator) b(key string) bool {
	value, _ := a.part[key].(bool)
	return value
}

func (a partApplicator) resetMetadataOn(enabled bool) {
	if enabled {
		a.writer.MessageMetadata(a.ctx, nil)
	}
}

func (a partApplicator) messageMetadata() {
	metadata, _ := a.part["messageMetadata"].(map[string]any)
	if len(metadata) > 0 {
		a.writer.MessageMetadata(a.ctx, metadata)
		return
	}
	a.resetMetadataOn(a.opts.ResetMetadataOnEmptyMessageMeta)
}

func (a partApplicator) textDelta() {
	if delta := a.s("delta"); delta != "" {
		a.writer.TextDelta(a.ctx, delta)
		return
	}
	a.resetMetadataOn(a.opts.ResetMetadataOnEmptyTextDelta)
}

func (a partApplicator) reasoningDelta() {
	if delta := a.s("delta"); delta != "" {
		a.writer.ReasoningDelta(a.ctx, delta)
		return
	}
	a.resetMetadataOn(a.opts.ResetMetadataOnEmptyTextDelta)
}

func (a partApplicator) sourceDocument() citations.SourceDocument {
	return citations.SourceDocument{
		ID:        a.s("sourceId"),
		Title:     a.s("title"),
		MediaType: a.s("mediaType"),
		Filename:  a.s("filename"),
	}
}

func (a partApplicator) sourceURL() citations.SourceCitation {
	return citations.SourceCitation{
		URL:   a.s("url"),
		Title: a.s("title"),
	}
}
