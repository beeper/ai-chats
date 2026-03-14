package sdk

import (
	"context"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote/pkg/shared/citations"
	"github.com/beeper/agentremote/pkg/shared/streamui"
)

// SemanticStream applies SDK-owned semantic stream operations onto a UI state.
// Bridges can use this without constructing a full Turn.
type SemanticStream struct {
	State   *streamui.UIState
	Emitter *streamui.Emitter
	Portal  *bridgev2.Portal
}

type semanticStreamAccessor struct {
	stream *SemanticStream
}

func (a *semanticStreamAccessor) valid() bool {
	return a != nil && a.stream != nil && a.stream.valid()
}

func (s *SemanticStream) valid() bool {
	return s != nil && s.State != nil && s.Emitter != nil
}

func (s *SemanticStream) MessageMetadata(ctx context.Context, metadata map[string]any) {
	if !s.valid() {
		return
	}
	s.Emitter.EmitUIMessageMetadata(ctx, s.Portal, metadata)
}

func (s *SemanticStream) Start(ctx context.Context, metadata map[string]any) {
	if !s.valid() {
		return
	}
	s.Emitter.EmitUIStart(ctx, s.Portal, metadata)
}

func (s *SemanticStream) StepStart(ctx context.Context) {
	if !s.valid() {
		return
	}
	s.Emitter.EmitUIStepStart(ctx, s.Portal)
}

func (s *SemanticStream) StepFinish(ctx context.Context) {
	if !s.valid() {
		return
	}
	s.Emitter.EmitUIStepFinish(ctx, s.Portal)
}

func (s *SemanticStream) TextDelta(ctx context.Context, delta string) {
	if !s.valid() {
		return
	}
	s.Emitter.EmitUITextDelta(ctx, s.Portal, delta)
}

func (s *SemanticStream) ReasoningDelta(ctx context.Context, delta string) {
	if !s.valid() {
		return
	}
	s.Emitter.EmitUIReasoningDelta(ctx, s.Portal, delta)
}

func (s *SemanticStream) Error(ctx context.Context, errText string) {
	if !s.valid() {
		return
	}
	s.Emitter.EmitUIError(ctx, s.Portal, errText)
}

func (s *SemanticStream) Abort(ctx context.Context, reason string) {
	if !s.valid() {
		return
	}
	s.Emitter.EmitUIAbort(ctx, s.Portal, reason)
}

type SemanticToolsController struct {
	semanticStreamAccessor
}

type SemanticApprovalController struct {
	semanticStreamAccessor
}

func (s *SemanticStream) Tools() *SemanticToolsController {
	if s == nil {
		return nil
	}
	return &SemanticToolsController{semanticStreamAccessor{stream: s}}
}

func (s *SemanticStream) Approvals() *SemanticApprovalController {
	if s == nil {
		return nil
	}
	return &SemanticApprovalController{semanticStreamAccessor{stream: s}}
}

func (c *SemanticToolsController) EnsureInputStart(ctx context.Context, toolCallID string, input any, opts ToolInputOptions) {
	if !c.valid() {
		return
	}
	displayTitle := opts.DisplayTitle
	if displayTitle == "" {
		displayTitle = opts.ToolName
	}
	c.stream.Emitter.EnsureUIToolInputStart(ctx, c.stream.Portal, toolCallID, opts.ToolName, opts.ProviderExecuted, displayTitle, nil)
	if input != nil {
		c.stream.Emitter.EmitUIToolInputAvailable(ctx, c.stream.Portal, toolCallID, opts.ToolName, input, opts.ProviderExecuted)
	}
}

func (c *SemanticToolsController) InputDelta(ctx context.Context, toolCallID, toolName, delta string, providerExecuted bool) {
	if !c.valid() {
		return
	}
	c.stream.Emitter.EmitUIToolInputDelta(ctx, c.stream.Portal, toolCallID, toolName, delta, providerExecuted)
}

func (c *SemanticToolsController) Input(ctx context.Context, toolCallID, toolName string, input any, providerExecuted bool) {
	if !c.valid() {
		return
	}
	c.stream.Emitter.EmitUIToolInputAvailable(ctx, c.stream.Portal, toolCallID, toolName, input, providerExecuted)
}

func (c *SemanticToolsController) InputError(ctx context.Context, toolCallID, toolName, rawInput, errText string, providerExecuted bool) {
	if !c.valid() {
		return
	}
	c.stream.Emitter.EmitUIToolInputError(ctx, c.stream.Portal, toolCallID, toolName, rawInput, errText, providerExecuted)
}

func (c *SemanticToolsController) Output(ctx context.Context, toolCallID string, output any, opts ToolOutputOptions) {
	if !c.valid() {
		return
	}
	c.stream.Emitter.EmitUIToolOutputAvailable(ctx, c.stream.Portal, toolCallID, output, opts.ProviderExecuted, opts.Streaming)
}

func (c *SemanticToolsController) OutputError(ctx context.Context, toolCallID, errText string, providerExecuted bool) {
	if !c.valid() {
		return
	}
	c.stream.Emitter.EmitUIToolOutputError(ctx, c.stream.Portal, toolCallID, errText, providerExecuted)
}

func (c *SemanticToolsController) Denied(ctx context.Context, toolCallID string) {
	if !c.valid() {
		return
	}
	c.stream.Emitter.EmitUIToolOutputDenied(ctx, c.stream.Portal, toolCallID)
}

func (a *SemanticApprovalController) EmitRequest(ctx context.Context, approvalID, toolCallID string) {
	if !a.valid() {
		return
	}
	a.stream.Emitter.EmitUIToolApprovalRequest(ctx, a.stream.Portal, approvalID, toolCallID)
}

func (a *SemanticApprovalController) Respond(ctx context.Context, approvalID, toolCallID string, approved bool, reason string) {
	if !a.valid() {
		return
	}
	a.stream.Emitter.EmitUIToolApprovalResponse(ctx, a.stream.Portal, approvalID, toolCallID, approved, reason)
	streamui.RecordApprovalResponse(a.stream.State, approvalID, toolCallID, approved, reason)
}

func (s *SemanticStream) File(ctx context.Context, url, mediaType string) {
	if !s.valid() {
		return
	}
	s.Emitter.EmitUIFile(ctx, s.Portal, url, mediaType)
}

func (s *SemanticStream) SourceURL(ctx context.Context, citation citations.SourceCitation) {
	if !s.valid() {
		return
	}
	s.Emitter.EmitUISourceURL(ctx, s.Portal, citation)
}

func (s *SemanticStream) SourceDocument(ctx context.Context, document citations.SourceDocument) {
	if !s.valid() {
		return
	}
	s.Emitter.EmitUISourceDocument(ctx, s.Portal, document)
}
