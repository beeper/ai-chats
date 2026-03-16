package ai

import (
	"context"
	"fmt"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote"
	airuntime "github.com/beeper/agentremote/pkg/runtime"
	bridgesdk "github.com/beeper/agentremote/sdk"
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

const (
	approvalMetadataKeyToolKind     = "tool_kind"
	approvalMetadataKeyRuleToolName = "rule_tool_name"
	approvalMetadataKeyServerLabel  = "server_label"
	approvalMetadataKeyAction       = "action"
)

func resolveApprovalID(approvalID string) string {
	approvalID = strings.TrimSpace(approvalID)
	if approvalID != "" {
		return approvalID
	}
	return NewCallID()
}

func (oc *AIClient) resolveApprovalTTL(ttl time.Duration) time.Duration {
	if ttl > 0 {
		return ttl
	}
	if oc == nil {
		return agentremote.DefaultApprovalExpiry
	}
	ttl = time.Duration(oc.toolApprovalsTTLSeconds()) * time.Second
	if ttl > 0 {
		return ttl
	}
	return agentremote.DefaultApprovalExpiry
}

func resolveApprovalPresentation(toolName string, presentation *agentremote.ApprovalPromptPresentation) agentremote.ApprovalPromptPresentation {
	if presentation != nil {
		return *presentation
	}
	return agentremote.ApprovalPromptPresentation{
		Title:       strings.TrimSpace(toolName),
		AllowAlways: true,
	}
}

func applyApprovalRequestMetadata(params *ToolApprovalParams, metadata map[string]any) {
	if params == nil || len(metadata) == 0 {
		return
	}
	if toolKind, ok := metadata[approvalMetadataKeyToolKind].(string); ok {
		params.ToolKind = ToolApprovalKind(strings.TrimSpace(toolKind))
	}
	if ruleToolName, ok := metadata[approvalMetadataKeyRuleToolName].(string); ok {
		params.RuleToolName = strings.TrimSpace(ruleToolName)
	}
	if serverLabel, ok := metadata[approvalMetadataKeyServerLabel].(string); ok {
		params.ServerLabel = strings.TrimSpace(serverLabel)
	}
	if action, ok := metadata[approvalMetadataKeyAction].(string); ok {
		params.Action = strings.TrimSpace(action)
	}
}

func approvalWaitReason(ctx context.Context) string {
	if ctx != nil && ctx.Err() != nil {
		return agentremote.ApprovalReasonCancelled
	}
	return agentremote.ApprovalReasonTimeout
}

func resolveApprovalPromptContext(state *streamingState, turn *bridgesdk.Turn, fallbackTurnID string) (string, id.EventID) {
	turnID := strings.TrimSpace(fallbackTurnID)
	replyTo := id.EventID("")
	if turn != nil && turn.ID() != "" {
		turnID = turn.ID()
	}
	if state == nil || state.turn == nil {
		return turnID, replyTo
	}
	if state.turn.ID() != "" {
		turnID = state.turn.ID()
	}
	return turnID, state.turn.InitialEventID()
}

type aiTurnApprovalHandle struct {
	client     *AIClient
	turn       *bridgesdk.Turn
	approvalID string
	toolCallID string
}

func (h *aiTurnApprovalHandle) ID() string {
	if h == nil {
		return ""
	}
	return h.approvalID
}

func (h *aiTurnApprovalHandle) ToolCallID() string {
	if h == nil {
		return ""
	}
	return h.toolCallID
}

func (h *aiTurnApprovalHandle) Wait(ctx context.Context) (bridgesdk.ToolApprovalResponse, error) {
	if h == nil || h.client == nil {
		return bridgesdk.ToolApprovalResponse{}, nil
	}
	resolution, _, ok := h.client.waitToolApproval(ctx, h.approvalID)
	decision := resolution.Decision
	if !ok && decision.Reason == "" {
		decision = airuntime.ToolApprovalDecision{State: airuntime.ToolApprovalTimedOut, Reason: approvalWaitReason(ctx)}
	}
	approved := approvalAllowed(decision)
	if h.turn != nil {
		h.turn.Approvals().Respond(h.turn.Context(), h.approvalID, h.toolCallID, approved, decision.Reason)
		if !approved {
			h.turn.Writer().Tools().Denied(h.turn.Context(), h.toolCallID)
		}
	}
	return bridgesdk.ToolApprovalResponse{
		Approved: approved,
		Always:   resolution.Always,
		Reason:   decision.Reason,
	}, nil
}

func newAITurnApprovalHandle(client *AIClient, turn *bridgesdk.Turn, approvalID, toolCallID string) *aiTurnApprovalHandle {
	return &aiTurnApprovalHandle{
		client:     client,
		turn:       turn,
		approvalID: strings.TrimSpace(approvalID),
		toolCallID: strings.TrimSpace(toolCallID),
	}
}

func (oc *AIClient) approvalParamsFromRequest(portal *bridgev2.Portal, state *streamingState, turn *bridgesdk.Turn, req bridgesdk.ApprovalRequest) ToolApprovalParams {
	params := ToolApprovalParams{
		ApprovalID:   resolveApprovalID(req.ApprovalID),
		ToolCallID:   strings.TrimSpace(req.ToolCallID),
		ToolName:     strings.TrimSpace(req.ToolName),
		Presentation: resolveApprovalPresentation(req.ToolName, req.Presentation),
		TTL:          oc.resolveApprovalTTL(req.TTL),
	}
	if portal != nil {
		params.RoomID = portal.MXID
	}
	if state != nil && state.turn != nil {
		params.TurnID = state.turn.ID()
	}
	if turn != nil {
		params.TurnID = turn.ID()
	}
	applyApprovalRequestMetadata(&params, req.Metadata)
	return params
}

func (oc *AIClient) startTurnApproval(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	turn *bridgesdk.Turn,
	params ToolApprovalParams,
	sendPrompt bool,
) (bridgesdk.ApprovalHandle, bool) {
	handle := newAITurnApprovalHandle(oc, turn, params.ApprovalID, params.ToolCallID)
	if oc == nil {
		return handle, false
	}
	if _, created := oc.registerToolApproval(params); !created {
		return handle, false
	}
	if turn != nil {
		turn.Approvals().EmitRequest(turn.Context(), params.ApprovalID, params.ToolCallID)
	}
	if !sendPrompt {
		return handle, true
	}
	if portal == nil || portal.MXID == "" || oc.UserLogin == nil || oc.UserLogin.UserMXID == "" || oc.approvalFlow == nil {
		_ = oc.resolveToolApproval(params.ApprovalID, false, agentremote.ApprovalReasonDeliveryError)
		return handle, true
	}
	turnID, replyTo := resolveApprovalPromptContext(state, turn, params.TurnID)
	oc.approvalFlow.SendPrompt(ctx, portal, agentremote.SendPromptParams{
		ApprovalPromptMessageParams: agentremote.ApprovalPromptMessageParams{
			ApprovalID:     params.ApprovalID,
			ToolCallID:     params.ToolCallID,
			ToolName:       params.ToolName,
			TurnID:         turnID,
			Presentation:   params.Presentation,
			ReplyToEventID: replyTo,
			ExpiresAt:      time.Now().Add(params.TTL),
		},
		RoomID:    portal.MXID,
		OwnerMXID: oc.UserLogin.UserMXID,
	})
	return handle, true
}

func (oc *AIClient) requestTurnApproval(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	turn *bridgesdk.Turn,
	req bridgesdk.ApprovalRequest,
) bridgesdk.ApprovalHandle {
	if oc == nil {
		return newAITurnApprovalHandle(nil, nil, req.ApprovalID, req.ToolCallID)
	}
	params := oc.approvalParamsFromRequest(portal, state, turn, req)
	handle, _ := oc.startTurnApproval(ctx, portal, state, turn, params, true)
	return handle
}

func (oc *AIClient) registerToolApproval(params ToolApprovalParams) (*agentremote.Pending[*pendingToolApprovalData], bool) {
	if oc == nil || oc.approvalFlow == nil {
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

func (oc *AIClient) resolveToolApproval(approvalID string, approved bool, reason string) error {
	if oc == nil || oc.approvalFlow == nil {
		return fmt.Errorf("approval flow unavailable")
	}
	approvalID = strings.TrimSpace(approvalID)
	if approvalID == "" {
		return fmt.Errorf("approval ID is required")
	}
	return oc.approvalFlow.Resolve(approvalID, agentremote.ApprovalDecisionPayload{
		ApprovalID: approvalID,
		Approved:   approved,
		Reason:     strings.TrimSpace(reason),
	})
}

func (oc *AIClient) waitToolApproval(ctx context.Context, approvalID string) (toolApprovalResolution, *pendingToolApprovalData, bool) {
	if oc == nil || oc.approvalFlow == nil {
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
		reason := approvalWaitReason(ctx)
		state := airuntime.ToolApprovalDenied
		if reason == agentremote.ApprovalReasonTimeout {
			oc.approvalFlow.FinishResolved(approvalID, agentremote.ApprovalDecisionPayload{
				ApprovalID: approvalID,
				Reason:     reason,
			})
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

func (oc *AIClient) waitForToolApprovalDecision(
	ctx context.Context,
	state *streamingState,
	handle bridgesdk.ApprovalHandle,
) airuntime.ToolApprovalDecision {
	if handle == nil {
		return airuntime.ToolApprovalDecision{State: airuntime.ToolApprovalTimedOut, Reason: approvalWaitReason(ctx)}
	}
	resp, err := handle.Wait(ctx)
	if err != nil {
		return airuntime.ToolApprovalDecision{State: airuntime.ToolApprovalDenied, Reason: err.Error()}
	}
	decision := airuntime.ToolApprovalDecision{State: airuntime.ToolApprovalDenied, Reason: strings.TrimSpace(resp.Reason)}
	if resp.Approved {
		decision.State = airuntime.ToolApprovalApproved
	}
	if !resp.Approved && decision.Reason == "" {
		decision.State = airuntime.ToolApprovalTimedOut
		decision.Reason = agentremote.ApprovalReasonTimeout
	}
	return decision
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
	if state == nil || state.turn == nil || tool == nil {
		return true
	}
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
	handle := state.turn.Approvals().Request(bridgesdk.ApprovalRequest{
		ApprovalID:   approvalID,
		ToolCallID:   tool.callID,
		ToolName:     toolName,
		Presentation: &presentation,
		TTL:          ttl,
		Metadata: map[string]any{
			approvalMetadataKeyToolKind:     string(ToolApprovalKindBuiltin),
			approvalMetadataKeyRuleToolName: toolName,
			approvalMetadataKeyAction:       action,
		},
	})
	if handle == nil {
		return true
	}
	decision := oc.waitForToolApprovalDecision(ctx, state, handle)
	return !approvalAllowed(decision)
}
