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
	providerExecuted, dynamic bool,
	title string,
	providerMetadata map[string]any,
) {
	if toolCallID == "" {
		return
	}
	if !e.State.UIToolStarted[toolCallID] {
		e.State.UIToolStarted[toolCallID] = true
		if strings.TrimSpace(toolName) != "" {
			e.State.UIToolNameByToolCallID[toolCallID] = toolName
		}
		part := map[string]any{
			"type":             "tool-input-start",
			"toolCallId":       toolCallID,
			"toolName":         toolName,
			"providerExecuted": providerExecuted,
		}
		if dynamic {
			part["dynamic"] = true
		}
		if strings.TrimSpace(title) != "" {
			part["title"] = title
		}
		if len(providerMetadata) > 0 {
			part["providerMetadata"] = providerMetadata
		}
		e.Emit(ctx, portal, part)
	}
	if strings.TrimSpace(toolName) != "" {
		e.State.UIToolNameByToolCallID[toolCallID] = toolName
	}
}

// EmitUIToolInputDelta sends a "tool-input-delta" event.
func (e *Emitter) EmitUIToolInputDelta(ctx context.Context, portal *bridgev2.Portal, toolCallID, toolName, delta string, providerExecuted bool) {
	if toolCallID == "" {
		return
	}
	e.EnsureUIToolInputStart(ctx, portal, toolCallID, toolName, providerExecuted, false, ToolDisplayTitle(toolName), nil)
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
	if toolCallID == "" {
		return
	}
	e.EnsureUIToolInputStart(ctx, portal, toolCallID, toolName, providerExecuted, false, ToolDisplayTitle(toolName), nil)
	e.Emit(ctx, portal, map[string]any{
		"type":             "tool-input-available",
		"toolCallId":       toolCallID,
		"toolName":         toolName,
		"input":            input,
		"providerExecuted": providerExecuted,
	})
}

// EmitUIToolInputError sends a "tool-input-error" event.
func (e *Emitter) EmitUIToolInputError(
	ctx context.Context,
	portal *bridgev2.Portal,
	toolCallID, toolName string,
	input any,
	errorText string,
	providerExecuted, dynamic bool,
) {
	if toolCallID == "" {
		return
	}
	e.EnsureUIToolInputStart(ctx, portal, toolCallID, toolName, providerExecuted, dynamic, ToolDisplayTitle(toolName), nil)
	part := map[string]any{
		"type":             "tool-input-error",
		"toolCallId":       toolCallID,
		"toolName":         toolName,
		"input":            input,
		"errorText":        errorText,
		"providerExecuted": providerExecuted,
	}
	if dynamic {
		part["dynamic"] = true
	}
	e.Emit(ctx, portal, part)
}

// EmitUIToolApprovalRequest sends a "tool-approval-request" event.
func (e *Emitter) EmitUIToolApprovalRequest(
	ctx context.Context,
	portal *bridgev2.Portal,
	approvalID, toolCallID, toolName string,
	ttlSeconds int,
) {
	if strings.TrimSpace(approvalID) == "" || strings.TrimSpace(toolCallID) == "" {
		return
	}
	if e.State == nil {
		return
	}
	e.State.UIToolCallIDByApproval[approvalID] = toolCallID
	e.Emit(ctx, portal, map[string]any{
		"type":       "tool-approval-request",
		"approvalId": approvalID,
		"toolCallId": toolCallID,
		"toolName":   toolName,
		"ttlSeconds": ttlSeconds,
	})
}

// EmitUIToolOutputAvailable sends a "tool-output-available" event.
func (e *Emitter) EmitUIToolOutputAvailable(ctx context.Context, portal *bridgev2.Portal, toolCallID string, output any, providerExecuted, preliminary bool) {
	if toolCallID == "" {
		return
	}
	if e.State != nil && !preliminary {
		if e.State.UIToolOutputFinalized[toolCallID] {
			return
		}
		e.State.UIToolOutputFinalized[toolCallID] = true
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
	if strings.TrimSpace(toolCallID) == "" {
		return
	}
	if e.State != nil {
		if e.State.UIToolOutputFinalized[toolCallID] {
			return
		}
		e.State.UIToolOutputFinalized[toolCallID] = true
	}
	e.Emit(ctx, portal, map[string]any{
		"type":       "tool-output-denied",
		"toolCallId": toolCallID,
	})
}

// EmitUIToolOutputError sends a "tool-output-error" event.
func (e *Emitter) EmitUIToolOutputError(ctx context.Context, portal *bridgev2.Portal, toolCallID, errorText string, providerExecuted bool) {
	if toolCallID == "" {
		return
	}
	if e.State != nil {
		if e.State.UIToolOutputFinalized[toolCallID] {
			return
		}
		e.State.UIToolOutputFinalized[toolCallID] = true
	}
	e.Emit(ctx, portal, map[string]any{
		"type":             "tool-output-error",
		"toolCallId":       toolCallID,
		"errorText":        errorText,
		"providerExecuted": providerExecuted,
	})
}

// ToolDisplayTitle returns toolName or a fallback "tool" for display.
func ToolDisplayTitle(toolName string) string {
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		return "tool"
	}
	return toolName
}
