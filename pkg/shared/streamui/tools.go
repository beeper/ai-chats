package streamui

import (
	"context"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
)

// EnsureUIToolInputStart sends "tool-input-start" once per toolCallID.
func (e *Emitter) EnsureUIToolInputStart(
	ctx context.Context,
	portal *bridgev2.Portal,
	toolCallID, toolName string,
	providerExecuted bool,
	title string,
	providerMetadata map[string]any,
) {
	if e.State == nil {
		return
	}
	toolCallID = strings.TrimSpace(toolCallID)
	if toolCallID == "" {
		return
	}
	if strings.TrimSpace(toolName) != "" {
		e.State.UIToolNameByToolCallID[toolCallID] = toolName
	}
	if e.State.UIToolStarted[toolCallID] {
		return
	}
	e.State.UIToolStarted[toolCallID] = true
	part := map[string]any{
		"type":             "tool-input-start",
		"toolCallId":       toolCallID,
		"toolName":         toolName,
		"providerExecuted": providerExecuted,
		"dynamic":          true,
	}
	if strings.TrimSpace(title) != "" {
		part["title"] = title
	}
	if len(providerMetadata) > 0 {
		part["providerMetadata"] = providerMetadata
	}
	e.Emit(ctx, portal, part)
}

// EmitUIToolInputDelta sends a "tool-input-delta" event.
func (e *Emitter) EmitUIToolInputDelta(ctx context.Context, portal *bridgev2.Portal, toolCallID, toolName, delta string, providerExecuted bool) {
	toolCallID = strings.TrimSpace(toolCallID)
	if toolCallID == "" {
		return
	}
	e.EnsureUIToolInputStart(ctx, portal, toolCallID, toolName, providerExecuted, ToolDisplayTitle(toolName), nil)
	if delta != "" {
		e.Emit(ctx, portal, map[string]any{
			"type":           "tool-input-delta",
			"toolCallId":     toolCallID,
			"inputTextDelta": delta,
		})
	}
}

// EmitUIToolInputAvailable sends a "tool-input-available" event.
func (e *Emitter) EmitUIToolInputAvailable(ctx context.Context, portal *bridgev2.Portal, toolCallID, toolName string, input any, providerExecuted bool) {
	toolCallID = strings.TrimSpace(toolCallID)
	if toolCallID == "" {
		return
	}
	e.EnsureUIToolInputStart(ctx, portal, toolCallID, toolName, providerExecuted, ToolDisplayTitle(toolName), nil)
	e.Emit(ctx, portal, map[string]any{
		"type":             "tool-input-available",
		"toolCallId":       toolCallID,
		"toolName":         toolName,
		"input":            input,
		"providerExecuted": providerExecuted,
		"dynamic":          true,
	})
}

// EmitUIToolInputError sends a "tool-input-error" event.
func (e *Emitter) EmitUIToolInputError(
	ctx context.Context,
	portal *bridgev2.Portal,
	toolCallID, toolName string,
	input any,
	errorText string,
	providerExecuted bool,
) {
	toolCallID = strings.TrimSpace(toolCallID)
	if toolCallID == "" {
		return
	}
	e.EnsureUIToolInputStart(ctx, portal, toolCallID, toolName, providerExecuted, ToolDisplayTitle(toolName), nil)
	part := map[string]any{
		"type":             "tool-input-error",
		"toolCallId":       toolCallID,
		"toolName":         toolName,
		"input":            input,
		"errorText":        errorText,
		"providerExecuted": providerExecuted,
		"dynamic":          true,
	}
	e.Emit(ctx, portal, part)
}

// EmitUIToolApprovalRequest sends a "tool-approval-request" event.
func (e *Emitter) EmitUIToolApprovalRequest(
	ctx context.Context,
	portal *bridgev2.Portal,
	approvalID, toolCallID string,
) {
	if strings.TrimSpace(approvalID) == "" || strings.TrimSpace(toolCallID) == "" {
		return
	}
	if e.State == nil {
		return
	}
	e.State.UIToolApprovalRequested[approvalID] = true
	e.State.UIToolCallIDByApproval[approvalID] = toolCallID
	e.Emit(ctx, portal, map[string]any{
		"type":       "tool-approval-request",
		"approvalId": approvalID,
		"toolCallId": toolCallID,
	})
}

// EmitUIToolApprovalResponse sends a "tool-approval-response" event.
func (e *Emitter) EmitUIToolApprovalResponse(
	ctx context.Context,
	portal *bridgev2.Portal,
	approvalID, toolCallID string,
	approved bool,
	reason string,
) {
	approvalID = strings.TrimSpace(approvalID)
	toolCallID = strings.TrimSpace(toolCallID)
	if approvalID == "" {
		return
	}
	if toolCallID == "" && e.State != nil {
		toolCallID = strings.TrimSpace(e.State.UIToolCallIDByApproval[approvalID])
	}
	if toolCallID == "" {
		return
	}
	part := map[string]any{
		"type":       "tool-approval-response",
		"approvalId": approvalID,
		"toolCallId": toolCallID,
		"approved":   approved,
	}
	if trimmedReason := strings.TrimSpace(reason); trimmedReason != "" {
		part["reason"] = trimmedReason
	}
	e.Emit(ctx, portal, part)
}

// markToolOutputFinalized returns true if the tool call has already been
// finalized (and should be skipped). Otherwise it marks it as finalized.
func (e *Emitter) markToolOutputFinalized(toolCallID string) bool {
	if e.State == nil {
		return false
	}
	if e.State.UIToolOutputFinalized[toolCallID] {
		return true
	}
	e.State.UIToolOutputFinalized[toolCallID] = true
	return false
}

// EmitUIToolOutputAvailable sends a "tool-output-available" event.
func (e *Emitter) EmitUIToolOutputAvailable(ctx context.Context, portal *bridgev2.Portal, toolCallID string, output any, providerExecuted, preliminary bool) {
	toolCallID = strings.TrimSpace(toolCallID)
	if toolCallID == "" {
		return
	}
	if !preliminary && e.markToolOutputFinalized(toolCallID) {
		return
	}
	part := map[string]any{
		"type":             "tool-output-available",
		"toolCallId":       toolCallID,
		"output":           output,
		"providerExecuted": providerExecuted,
	}
	if preliminary {
		part["preliminary"] = true
	}
	e.Emit(ctx, portal, part)
}

// EmitUIToolOutputDenied sends a "tool-output-denied" event.
func (e *Emitter) EmitUIToolOutputDenied(ctx context.Context, portal *bridgev2.Portal, toolCallID string) {
	toolCallID = strings.TrimSpace(toolCallID)
	if toolCallID == "" {
		return
	}
	if e.markToolOutputFinalized(toolCallID) {
		return
	}
	e.Emit(ctx, portal, map[string]any{
		"type":       "tool-output-denied",
		"toolCallId": toolCallID,
	})
}

// EmitUIToolOutputError sends a "tool-output-error" event.
func (e *Emitter) EmitUIToolOutputError(ctx context.Context, portal *bridgev2.Portal, toolCallID, errorText string, providerExecuted bool) {
	toolCallID = strings.TrimSpace(toolCallID)
	if toolCallID == "" {
		return
	}
	if e.markToolOutputFinalized(toolCallID) {
		return
	}
	e.Emit(ctx, portal, map[string]any{
		"type":             "tool-output-error",
		"toolCallId":       toolCallID,
		"errorText":        errorText,
		"providerExecuted": providerExecuted,
	})
}

// ToolDisplayTitle returns toolName, its annotation title if available, or a
// fallback "tool" for display.
func ToolDisplayTitle(toolName string) string {
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		return "tool"
	}
	return toolName
}
