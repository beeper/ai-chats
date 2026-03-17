package sdk

import (
	"context"
	"strings"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote/pkg/shared/citations"
	"github.com/beeper/agentremote/pkg/shared/streamui"
)

// ToolInputOptions controls how a tool input start is represented in the SDK UI stream.
type ToolInputOptions struct {
	ToolName         string
	ProviderExecuted bool
	DisplayTitle     string
	Extra            map[string]any
}

// ToolOutputOptions controls how a tool output is represented in the SDK UI stream.
type ToolOutputOptions struct {
	ProviderExecuted bool
	Streaming        bool
	Extra            map[string]any
}

// Writer emits semantic turn parts onto a streamui emitter.
//
// This is the canonical write surface for both SDK-managed turns and bridge-
// managed streaming state. Direct emitter access should be reserved for rare
// raw-part escape hatches only.
type Writer struct {
	State   *streamui.UIState
	Emitter *streamui.Emitter
	Portal  *bridgev2.Portal

	ensureStarted func()
	onText        func(string)
	onMetadata    func(map[string]any)
}

func (w *Writer) valid() bool {
	return w != nil && w.State != nil && w.Emitter != nil
}

func (w *Writer) ready() bool {
	if !w.valid() {
		return false
	}
	if w.ensureStarted != nil {
		w.ensureStarted()
	}
	return true
}

func emitCtx(ctx context.Context) context.Context {
	if ctx != nil {
		return ctx
	}
	return context.Background()
}

func (w *Writer) MessageMetadata(ctx context.Context, metadata map[string]any) {
	if !w.ready() {
		return
	}
	if w.onMetadata != nil {
		w.onMetadata(metadata)
	}
	w.Emitter.EmitUIMessageMetadata(emitCtx(ctx), w.Portal, metadata)
}

func (w *Writer) Start(ctx context.Context, metadata map[string]any) {
	if !w.valid() {
		return
	}
	w.Emitter.EmitUIStart(emitCtx(ctx), w.Portal, metadata)
}

func (w *Writer) StepStart(ctx context.Context) {
	if !w.ready() {
		return
	}
	w.Emitter.EmitUIStepStart(emitCtx(ctx), w.Portal)
}

func (w *Writer) StepFinish(ctx context.Context) {
	if !w.ready() {
		return
	}
	w.Emitter.EmitUIStepFinish(emitCtx(ctx), w.Portal)
}

func (w *Writer) TextDelta(ctx context.Context, delta string) {
	if !w.ready() {
		return
	}
	if w.onText != nil {
		w.onText(delta)
	}
	w.Emitter.EmitUITextDelta(emitCtx(ctx), w.Portal, delta)
}

func (w *Writer) FinishText(ctx context.Context) {
	if !w.ready() || w.State == nil || w.State.UITextID == "" {
		return
	}
	partID := w.State.UITextID
	w.Emitter.Emit(emitCtx(ctx), w.Portal, map[string]any{
		"type": "text-end",
		"id":   partID,
	})
	w.State.UITextID = ""
}

func (w *Writer) ReasoningDelta(ctx context.Context, delta string) {
	if !w.ready() {
		return
	}
	w.Emitter.EmitUIReasoningDelta(emitCtx(ctx), w.Portal, delta)
}

func (w *Writer) FinishReasoning(ctx context.Context) {
	if !w.ready() || w.State == nil || w.State.UIReasoningID == "" {
		return
	}
	partID := w.State.UIReasoningID
	w.Emitter.Emit(emitCtx(ctx), w.Portal, map[string]any{
		"type": "reasoning-end",
		"id":   partID,
	})
	w.State.UIReasoningID = ""
}

func (w *Writer) Error(ctx context.Context, errText string) {
	if !w.ready() {
		return
	}
	w.Emitter.EmitUIError(emitCtx(ctx), w.Portal, errText)
}

func (w *Writer) Finish(ctx context.Context, finishReason string, metadata map[string]any) {
	if !w.ready() {
		return
	}
	w.Emitter.EmitUIFinish(emitCtx(ctx), w.Portal, finishReason, metadata)
}

func (w *Writer) Abort(ctx context.Context, reason string) {
	if !w.ready() {
		return
	}
	w.Emitter.EmitUIAbort(emitCtx(ctx), w.Portal, reason)
}

func (w *Writer) File(ctx context.Context, url, mediaType string) {
	if !w.ready() {
		return
	}
	w.Emitter.EmitUIFile(emitCtx(ctx), w.Portal, url, mediaType)
}

func (w *Writer) SourceURL(ctx context.Context, citation citations.SourceCitation) {
	if !w.ready() {
		return
	}
	w.Emitter.EmitUISourceURL(emitCtx(ctx), w.Portal, citation)
}

func (w *Writer) SourceDocument(ctx context.Context, document citations.SourceDocument) {
	if !w.ready() {
		return
	}
	w.Emitter.EmitUISourceDocument(emitCtx(ctx), w.Portal, document)
}

// Data emits a bridge-specific custom event using the reserved data-* namespace.
func (w *Writer) Data(ctx context.Context, name string, payload any, transient bool) {
	if !w.ready() {
		return
	}
	partType := strings.TrimSpace(name)
	if partType == "" {
		return
	}
	if !strings.HasPrefix(partType, "data-") {
		partType = "data-" + partType
	}
	part := map[string]any{
		"type": partType,
		"data": payload,
	}
	if transient {
		part["transient"] = true
	}
	w.Emitter.Emit(emitCtx(ctx), w.Portal, part)
}

// RawPart emits an arbitrary stream part. This is the lowest-level escape hatch.
func (w *Writer) RawPart(ctx context.Context, part map[string]any) {
	if !w.ready() || len(part) == 0 {
		return
	}
	w.Emitter.Emit(emitCtx(ctx), w.Portal, part)
}

// Tools returns the writer's tool streaming controller.
func (w *Writer) Tools() *ToolsController {
	if w == nil {
		return nil
	}
	return &ToolsController{writer: w}
}

// Approvals returns the writer's approval controller.
func (w *Writer) Approvals() *ApprovalController {
	if w == nil {
		return nil
	}
	return &ApprovalController{writer: w}
}

type ToolsController struct {
	writer *Writer
}

func (c *ToolsController) valid() bool {
	return c != nil && c.writer != nil && c.writer.valid()
}

// EnsureInputStart ensures the tool input UI exists and optionally publishes input.
func (c *ToolsController) EnsureInputStart(ctx context.Context, toolCallID string, input any, opts ToolInputOptions) {
	if !c.valid() || strings.TrimSpace(toolCallID) == "" {
		return
	}
	c.writer.ready()
	toolName := strings.TrimSpace(opts.ToolName)
	displayTitle := strings.TrimSpace(opts.DisplayTitle)
	if displayTitle == "" {
		displayTitle = streamui.ToolDisplayTitle(toolName)
	}
	c.writer.Emitter.EnsureUIToolInputStart(emitCtx(ctx), c.writer.Portal, toolCallID, toolName, opts.ProviderExecuted, displayTitle, opts.Extra)
	if input != nil {
		c.writer.Emitter.EmitUIToolInputAvailable(emitCtx(ctx), c.writer.Portal, toolCallID, toolName, input, opts.ProviderExecuted)
	}
}

// InputDelta emits a tool input delta.
func (c *ToolsController) InputDelta(ctx context.Context, toolCallID, toolName, delta string, providerExecuted bool) {
	if !c.valid() {
		return
	}
	c.writer.ready()
	c.writer.Emitter.EmitUIToolInputDelta(emitCtx(ctx), c.writer.Portal, toolCallID, toolName, delta, providerExecuted)
}

// Input emits a complete tool input payload.
func (c *ToolsController) Input(ctx context.Context, toolCallID, toolName string, input any, providerExecuted bool) {
	if !c.valid() {
		return
	}
	c.writer.ready()
	c.writer.Emitter.EmitUIToolInputAvailable(emitCtx(ctx), c.writer.Portal, toolCallID, toolName, input, providerExecuted)
}

// InputError emits a tool input parsing error.
func (c *ToolsController) InputError(ctx context.Context, toolCallID, toolName, rawInput, errText string, providerExecuted bool) {
	if !c.valid() {
		return
	}
	c.writer.ready()
	c.writer.Emitter.EmitUIToolInputError(emitCtx(ctx), c.writer.Portal, toolCallID, toolName, rawInput, errText, providerExecuted)
}

// Output emits a tool output payload.
func (c *ToolsController) Output(ctx context.Context, toolCallID string, output any, opts ToolOutputOptions) {
	if !c.valid() {
		return
	}
	c.writer.ready()
	c.writer.Emitter.EmitUIToolOutputAvailable(emitCtx(ctx), c.writer.Portal, toolCallID, output, opts.ProviderExecuted, opts.Streaming)
}

// OutputError emits a tool error payload.
func (c *ToolsController) OutputError(ctx context.Context, toolCallID, errText string, providerExecuted bool) {
	if !c.valid() {
		return
	}
	c.writer.ready()
	c.writer.Emitter.EmitUIToolOutputError(emitCtx(ctx), c.writer.Portal, toolCallID, errText, providerExecuted)
}

// Denied emits a denied tool result.
func (c *ToolsController) Denied(ctx context.Context, toolCallID string) {
	if !c.valid() {
		return
	}
	c.writer.ready()
	c.writer.Emitter.EmitUIToolOutputDenied(emitCtx(ctx), c.writer.Portal, toolCallID)
}

type ApprovalController struct {
	writer *Writer
	turn   *Turn
}

func (a *ApprovalController) currentWriter() *Writer {
	if a == nil {
		return nil
	}
	if a.turn != nil {
		return a.turn.Writer()
	}
	return a.writer
}

// SetHandler configures a provider-specific approval handler for this turn.
func (a *ApprovalController) SetHandler(handler func(ctx context.Context, turn *Turn, req ApprovalRequest) ApprovalHandle) {
	if a == nil || a.turn == nil {
		return
	}
	a.turn.approvalRequester = handler
}

// Request creates a new approval request.
func (a *ApprovalController) Request(req ApprovalRequest) ApprovalHandle {
	if a == nil || a.turn == nil {
		return nil
	}
	return a.turn.requestApproval(req)
}

// EmitRequest emits the approval-request UI state for a provider-managed approval.
func (a *ApprovalController) EmitRequest(ctx context.Context, approvalID, toolCallID string) {
	w := a.currentWriter()
	if w == nil || !w.valid() {
		return
	}
	w.ready()
	w.Emitter.EmitUIToolApprovalRequest(emitCtx(ctx), w.Portal, approvalID, toolCallID)
}

// Respond emits the approval-response UI state for a provider-managed approval.
func (a *ApprovalController) Respond(ctx context.Context, approvalID, toolCallID string, approved bool, reason string) {
	w := a.currentWriter()
	if w == nil || !w.valid() {
		return
	}
	w.ready()
	w.Emitter.EmitUIToolApprovalResponse(emitCtx(ctx), w.Portal, approvalID, toolCallID, approved, reason)
	streamui.RecordApprovalResponse(w.State, approvalID, toolCallID, approved, reason)
}
