package sdk

import (
	"context"
	"strings"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote/pkg/shared/streamui"
)

// ToolInputOptions controls how a tool input start is represented in the SDK UI stream.
type ToolInputOptions struct {
	ToolName         string
	ProviderExecuted bool
	DisplayTitle     string
}

// ToolOutputOptions controls how a tool output is represented in the SDK UI stream.
type ToolOutputOptions struct {
	ProviderExecuted bool
	Streaming        bool
}

// turnAccessor provides shared valid/portal checks for turn-scoped controllers.
type turnAccessor struct {
	turn *Turn
}

func (a *turnAccessor) valid() bool { return a != nil && a.turn != nil }

func (a *turnAccessor) portal() *bridgev2.Portal {
	if !a.valid() || a.turn.conv == nil {
		return nil
	}
	return a.turn.conv.portal
}

// TurnStream is the provider-facing streaming surface for a turn.
type TurnStream struct {
	turnAccessor
}

// ToolsController is the turn-owned tool streaming surface.
type ToolsController struct {
	turnAccessor
}

// Stream returns the turn's provider-facing streaming surface.
func (t *Turn) Stream() *TurnStream {
	if t == nil {
		return nil
	}
	return &TurnStream{turnAccessor{turn: t}}
}

// Emitter returns the underlying stream emitter as an escape hatch.
func (s *TurnStream) Emitter() *streamui.Emitter {
	if !s.valid() {
		return nil
	}
	return s.turn.emitter
}

// SetTransport configures a custom transport for streamed turn events.
func (s *TurnStream) SetTransport(hook func(turnID string, seq int, content map[string]any, txnID string) bool) {
	if !s.valid() {
		return
	}
	s.turn.streamHook = hook
}

// Tools returns the turn's tool streaming controller.
func (t *Turn) Tools() *ToolsController {
	if t == nil {
		return nil
	}
	return &ToolsController{turnAccessor{turn: t}}
}

// EnsureInputStart ensures the tool input UI exists and optionally publishes input.
func (c *ToolsController) EnsureInputStart(toolCallID string, input any, opts ToolInputOptions) {
	if !c.valid() || strings.TrimSpace(toolCallID) == "" {
		return
	}
	c.turn.ensureStarted()
	toolName := strings.TrimSpace(opts.ToolName)
	displayTitle := strings.TrimSpace(opts.DisplayTitle)
	if displayTitle == "" {
		displayTitle = streamui.ToolDisplayTitle(toolName)
	}
	c.turn.emitter.EnsureUIToolInputStart(c.turn.turnCtx, c.portal(), toolCallID, toolName, opts.ProviderExecuted, displayTitle, nil)
	if input != nil {
		c.turn.emitter.EmitUIToolInputAvailable(c.turn.turnCtx, c.portal(), toolCallID, toolName, input, opts.ProviderExecuted)
	}
}

// InputDelta emits a tool input delta.
func (c *ToolsController) InputDelta(toolCallID, delta string, providerExecuted bool) {
	if !c.valid() {
		return
	}
	c.turn.ensureStarted()
	c.turn.emitter.EmitUIToolInputDelta(c.turn.turnCtx, c.portal(), toolCallID, "", delta, providerExecuted)
}

// Input emits a complete tool input payload.
func (c *ToolsController) Input(toolCallID, toolName string, input any, providerExecuted bool) {
	if !c.valid() {
		return
	}
	c.turn.ensureStarted()
	c.turn.emitter.EmitUIToolInputAvailable(c.turn.turnCtx, c.portal(), toolCallID, toolName, input, providerExecuted)
}

// Output emits a tool output payload.
func (c *ToolsController) Output(toolCallID string, output any, opts ToolOutputOptions) {
	if !c.valid() {
		return
	}
	c.turn.ensureStarted()
	c.turn.emitter.EmitUIToolOutputAvailable(c.turn.turnCtx, c.portal(), toolCallID, output, opts.ProviderExecuted, opts.Streaming)
}

// OutputError emits a tool error payload.
func (c *ToolsController) OutputError(toolCallID, errText string, providerExecuted bool) {
	if !c.valid() {
		return
	}
	c.turn.ensureStarted()
	c.turn.emitter.EmitUIToolOutputError(c.turn.turnCtx, c.portal(), toolCallID, errText, providerExecuted)
}

// Denied emits a denied tool result.
func (c *ToolsController) Denied(toolCallID string) {
	if !c.valid() {
		return
	}
	c.turn.ensureStarted()
	c.turn.emitter.EmitUIToolOutputDenied(c.turn.turnCtx, c.portal(), toolCallID)
}

// ApprovalController is the turn-owned approval surface.
type ApprovalController struct {
	turnAccessor
}

// Approvals returns the turn's approval controller.
func (t *Turn) Approvals() *ApprovalController {
	if t == nil {
		return nil
	}
	return &ApprovalController{turnAccessor{turn: t}}
}

// SetHandler configures a provider-specific approval handler for this turn.
func (a *ApprovalController) SetHandler(handler func(ctx context.Context, turn *Turn, req ApprovalRequest) ApprovalHandle) {
	if !a.valid() {
		return
	}
	a.turn.approvalRequester = handler
}

// Request creates a new approval request.
func (a *ApprovalController) Request(req ApprovalRequest) ApprovalHandle {
	if !a.valid() {
		return nil
	}
	return a.turn.requestApproval(req)
}

// EmitRequest emits the approval-request UI state for a provider-managed approval.
func (a *ApprovalController) EmitRequest(approvalID, toolCallID string) {
	if !a.valid() {
		return
	}
	a.turn.ensureStarted()
	a.turn.emitter.EmitUIToolApprovalRequest(a.turn.turnCtx, a.portal(), approvalID, toolCallID)
}

// Respond emits the approval-response UI state for a provider-managed approval.
func (a *ApprovalController) Respond(approvalID, toolCallID string, approved bool, reason string) {
	if !a.valid() {
		return
	}
	a.turn.ensureStarted()
	a.turn.emitter.EmitUIToolApprovalResponse(a.turn.turnCtx, a.portal(), approvalID, toolCallID, approved, reason)
}
