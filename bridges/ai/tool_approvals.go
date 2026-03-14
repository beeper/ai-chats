package ai

import (
	"context"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote"
	airuntime "github.com/beeper/agentremote/pkg/runtime"
)

type ToolApprovalKind string

const (
	ToolApprovalKindMCP     ToolApprovalKind = "mcp"
	ToolApprovalKindBuiltin ToolApprovalKind = "builtin"
)

type toolApprovalResolution struct {
	Decision airuntime.ToolApprovalDecision
	Always   bool // Persist allow rule when true (only meaningful when approved).
}

// pendingToolApprovalData holds bridge-specific metadata stored in
// ApprovalFlow's Pending.Data field.
type pendingToolApprovalData struct {
	ApprovalID string
	RoomID     id.RoomID
	TurnID     string

	ToolCallID string
	ToolName   string // display name (e.g. "message" or "mcp.<name>")

	ToolKind     ToolApprovalKind
	RuleToolName string // normalized for matching/persistence (e.g. "message" or raw MCP tool name without "mcp.")
	ServerLabel  string // MCP only
	Action       string // builtin only (optional)
	Presentation agentremote.ApprovalPromptPresentation

	RequestedAt time.Time
}

// ToolApprovalParams holds the parameters for registering a tool approval request.
type ToolApprovalParams struct {
	ApprovalID string
	RoomID     id.RoomID
	TurnID     string

	ToolCallID string
	ToolName   string

	ToolKind     ToolApprovalKind
	RuleToolName string
	ServerLabel  string
	Action       string
	Presentation agentremote.ApprovalPromptPresentation

	TTL time.Duration
}

func (oc *AIClient) registerToolApproval(params ToolApprovalParams) (*agentremote.Pending[*pendingToolApprovalData], bool) {
	if oc == nil {
		return nil, false
	}
	data := &pendingToolApprovalData{
		ApprovalID:   strings.TrimSpace(params.ApprovalID),
		RoomID:       params.RoomID,
		TurnID:       params.TurnID,
		ToolCallID:   strings.TrimSpace(params.ToolCallID),
		ToolName:     strings.TrimSpace(params.ToolName),
		ToolKind:     params.ToolKind,
		RuleToolName: strings.TrimSpace(params.RuleToolName),
		ServerLabel:  strings.TrimSpace(params.ServerLabel),
		Action:       strings.TrimSpace(params.Action),
		Presentation: params.Presentation,
		RequestedAt:  time.Now(),
	}
	p, created := oc.approvalFlow.Register(params.ApprovalID, params.TTL, data)
	if created {
		oc.Log().Debug().Str("approval_id", params.ApprovalID).Str("tool", params.ToolName).Dur("ttl", params.TTL).Msg("tool approval registered")
	}
	return p, created
}

func (oc *AIClient) waitToolApproval(ctx context.Context, approvalID string) (toolApprovalResolution, *pendingToolApprovalData, bool) {
	if oc == nil || oc.UserLogin == nil {
		return toolApprovalResolution{}, nil, false
	}
	approvalID = strings.TrimSpace(approvalID)
	if approvalID == "" {
		return toolApprovalResolution{}, nil, false
	}

	p := oc.approvalFlow.Get(approvalID)
	if p == nil {
		return toolApprovalResolution{}, nil, false
	}
	d := p.Data

	oc.Log().Debug().Str("approval_id", approvalID).Str("tool", d.ToolName).Msg("tool approval wait started")

	decision, ok := oc.approvalFlow.Wait(ctx, approvalID)
	if !ok {
		reason := agentremote.ApprovalReasonTimeout
		if ctx.Err() != nil {
			reason = agentremote.ApprovalReasonCancelled
		}
		oc.approvalFlow.FinishResolved(approvalID, agentremote.ApprovalDecisionPayload{
			ApprovalID: approvalID,
			Reason:     reason,
		})
		state := airuntime.ToolApprovalDenied
		if reason == agentremote.ApprovalReasonTimeout {
			state = airuntime.ToolApprovalTimedOut
		}
		resolution := toolApprovalResolution{
			Decision: airuntime.ToolApprovalDecision{State: state, Reason: reason},
		}
		oc.Log().Debug().Str("approval_id", approvalID).Str("tool", d.ToolName).Str("reason", reason).Msg("tool approval wait ended without decision")
		return resolution, d, false
	}

	// Convert ApprovalDecisionPayload to toolApprovalResolution.
	state := airuntime.ToolApprovalDenied
	if decision.Approved {
		state = airuntime.ToolApprovalApproved
	}
	resolution := toolApprovalResolution{
		Decision: airuntime.ToolApprovalDecision{State: state, Reason: decision.Reason},
		Always:   decision.Always,
	}

	oc.Log().Debug().Str("approval_id", approvalID).Str("tool", d.ToolName).Str("state", string(resolution.Decision.State)).Msg("tool approval decision received")
	if approvalAllowed(resolution.Decision) && resolution.Always {
		if err := oc.persistAlwaysAllow(ctx, d); err != nil {
			oc.Log().Warn().Err(err).Str("approval_id", approvalID).Msg("Failed to persist always-allow rule")
		}
	}
	oc.approvalFlow.FinishResolved(approvalID, decision)
	return resolution, d, true
}

func approvalAllowed(decision airuntime.ToolApprovalDecision) bool {
	return decision.State == airuntime.ToolApprovalApproved
}

// isBuiltinToolDenied checks whether a builtin tool call requires user approval
// and, if so, registers the approval, emits a UI request, and waits for a decision.
// Returns true if the tool call was denied and should not be executed.
func (oc *AIClient) isBuiltinToolDenied(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	tool *activeToolCall,
	toolName string,
	argsObj map[string]any,
) (denied bool) {
	if state == nil || tool == nil {
		return true
	}
	lifecycle := oc.toolLifecycle(portal, state)
	required, action := oc.builtinToolApprovalRequirement(toolName, argsObj)
	if required && oc.isBuiltinAlwaysAllowed(toolName, action) {
		required = false
	}
	if required && state.heartbeat != nil {
		required = false
	}
	input := airuntime.ToolPolicyInput{
		ToolName: strings.TrimSpace(toolName),
		ToolKind: "builtin",
		CallID:   strings.TrimSpace(tool.callID),
	}
	if required {
		input.RequiredTools = map[string]struct{}{strings.TrimSpace(toolName): {}}
	}
	runtimeDecision := airuntime.DecideToolApproval(input)
	required = runtimeDecision.State == airuntime.ToolApprovalRequired
	if !required {
		return false
	}
	approvalID := NewCallID()
	ttl := time.Duration(oc.toolApprovalsTTLSeconds()) * time.Second
	presentation := buildBuiltinApprovalPresentation(toolName, action, argsObj)
	if _, created := oc.registerToolApproval(ToolApprovalParams{
		ApprovalID:   approvalID,
		RoomID:       state.roomID,
		TurnID:       state.turnID,
		ToolCallID:   tool.callID,
		ToolName:     toolName,
		ToolKind:     ToolApprovalKindBuiltin,
		RuleToolName: toolName,
		Action:       action,
		Presentation: presentation,
		TTL:          ttl,
	}); !created {
		oc.loggerForContext(ctx).Error().
			Str("tool_name", toolName).
			Msg("tool approval: failed to register builtin approval request")
		return true
	}
	if !oc.emitUIToolApprovalRequest(ctx, portal, state, approvalID, tool.callID, toolName, presentation, "", oc.toolApprovalsTTLSeconds()) {
		decision := airuntime.ToolApprovalDecision{State: airuntime.ToolApprovalDenied, Reason: agentremote.ApprovalReasonDeliveryError}
		oc.approvalFlow.FinishResolved(approvalID, agentremote.ApprovalDecisionPayload{
			ApprovalID: approvalID,
			Reason:     agentremote.ApprovalReasonDeliveryError,
		})
		lifecycle.respondApproval(ctx, approvalID, tool.callID, false, decision.Reason)
		return true
	}
	resolution, _, ok := oc.waitToolApproval(ctx, approvalID)
	decision := resolution.Decision
	if !ok {
		if decision.Reason == "" {
			decision = airuntime.ToolApprovalDecision{State: airuntime.ToolApprovalTimedOut, Reason: agentremote.ApprovalReasonTimeout}
		}
	}
	lifecycle.respondApproval(ctx, approvalID, tool.callID, approvalAllowed(decision), decision.Reason)
	if !approvalAllowed(decision) {
		return true
	}
	return false
}
