package ai

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/id"

	airuntime "github.com/beeper/agentremote/pkg/runtime"
	"github.com/beeper/agentremote/pkg/shared/maputil"
	"github.com/beeper/agentremote/sdk"
)

type ToolApprovalKind string

const (
	ToolApprovalKindMCP     ToolApprovalKind = "mcp"
	ToolApprovalKindBuiltin ToolApprovalKind = "builtin"
)

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
	Presentation sdk.ApprovalPromptPresentation

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
	Presentation sdk.ApprovalPromptPresentation

	TTL time.Duration
}

const (
	approvalMetadataKeyToolKind     = "tool_kind"
	approvalMetadataKeyRuleToolName = "rule_tool_name"
	approvalMetadataKeyServerLabel  = "server_label"
	approvalMetadataKeyAction       = "action"
)

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

func resolveApprovalPromptContext(state *streamingState, turn *sdk.Turn, fallbackTurnID string) (string, id.EventID, id.EventID) {
	turnID := strings.TrimSpace(fallbackTurnID)
	replyTo := id.EventID("")
	threadRoot := id.EventID("")
	if turn != nil && turn.ID() != "" {
		turnID = turn.ID()
	}
	if state == nil || state.turn == nil {
		return turnID, replyTo, threadRoot
	}
	if state.turn.ID() != "" {
		turnID = state.turn.ID()
	}
	return turnID, state.turn.InitialEventID(), state.replyTarget.ThreadRoot
}

func normalizeApprovalToken(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func normalizeMcpRuleToolName(name string) string {
	n := normalizeApprovalToken(name)
	return strings.TrimPrefix(n, "mcp.")
}

func (oc *AIClient) toolApprovalsRuntimeEnabled() bool {
	if oc == nil || oc.connector == nil {
		return false
	}
	cfg := oc.connector.Config.ToolApprovals.WithDefaults()
	return cfg.Enabled != nil && *cfg.Enabled
}

func (oc *AIClient) toolApprovalsTTLSeconds() int {
	if oc == nil || oc.connector == nil {
		return 600
	}
	return oc.connector.Config.ToolApprovals.WithDefaults().TTLSeconds
}

func (oc *AIClient) toolApprovalsRequireForMCP() bool {
	if oc == nil || oc.connector == nil {
		return true
	}
	cfg := oc.connector.Config.ToolApprovals.WithDefaults()
	return cfg.RequireForMCP == nil || *cfg.RequireForMCP
}

func (oc *AIClient) toolApprovalsRequireForTool(toolName string) bool {
	if oc == nil || oc.connector == nil {
		return false
	}
	cfg := oc.connector.Config.ToolApprovals.WithDefaults()
	if cfg.RequireForTools == nil {
		return false
	}
	needle := normalizeApprovalToken(toolName)
	for _, raw := range cfg.RequireForTools {
		if normalizeApprovalToken(raw) == needle {
			return true
		}
	}
	return false
}

func (oc *AIClient) isMcpAlwaysAllowed(ctx context.Context, serverLabel, toolName string) bool {
	if oc == nil || oc.UserLogin == nil {
		return false
	}
	sl := normalizeApprovalToken(serverLabel)
	tn := normalizeMcpRuleToolName(toolName)
	if sl == "" || tn == "" {
		return false
	}
	return oc.hasToolApprovalRule(ctx, ToolApprovalKindMCP, sl, tn, "")
}

func (oc *AIClient) isBuiltinAlwaysAllowed(ctx context.Context, toolName, action string) bool {
	if oc == nil || oc.UserLogin == nil {
		return false
	}
	tn := normalizeApprovalToken(toolName)
	act := normalizeApprovalToken(action)
	if tn == "" {
		return false
	}
	return oc.hasBuiltinToolApprovalRule(ctx, tn, act)
}

func (oc *AIClient) persistAlwaysAllow(ctx context.Context, pending *pendingToolApprovalData) error {
	if oc == nil || oc.UserLogin == nil || pending == nil {
		return nil
	}
	switch pending.ToolKind {
	case ToolApprovalKindMCP:
		sl := normalizeApprovalToken(pending.ServerLabel)
		tn := normalizeMcpRuleToolName(pending.RuleToolName)
		if sl == "" || tn == "" {
			return nil
		}
		return oc.insertToolApprovalRule(ctx, ToolApprovalKindMCP, sl, tn, "")
	case ToolApprovalKindBuiltin:
		tn := normalizeApprovalToken(pending.RuleToolName)
		act := normalizeApprovalToken(pending.Action)
		if tn == "" {
			return nil
		}
		return oc.insertToolApprovalRule(ctx, ToolApprovalKindBuiltin, "", tn, act)
	default:
		return nil
	}
}

func (oc *AIClient) hasToolApprovalRule(ctx context.Context, toolKind ToolApprovalKind, serverLabel, toolName, action string) bool {
	scope := loginScopeForClient(oc)
	if scope == nil {
		return false
	}
	var matched int
	err := scope.db.QueryRow(ctx, `
		SELECT 1
		FROM aichats_tool_approval_rules
		WHERE bridge_id=$1 AND login_id=$2 AND tool_kind=$3 AND server_label=$4 AND tool_name=$5 AND action=$6
		LIMIT 1
	`, scope.bridgeID, scope.loginID, string(toolKind), serverLabel, toolName, action).Scan(&matched)
	if err == sql.ErrNoRows {
		return false
	}
	if err != nil {
		oc.Log().Warn().Err(err).Str("tool_kind", string(toolKind)).Str("tool_name", toolName).Msg("tool approvals: lookup failed")
		return false
	}
	return matched == 1
}

func (oc *AIClient) hasBuiltinToolApprovalRule(ctx context.Context, toolName, action string) bool {
	scope := loginScopeForClient(oc)
	if scope == nil {
		return false
	}
	var matched int
	err := scope.db.QueryRow(ctx, `
		SELECT 1
		FROM aichats_tool_approval_rules
		WHERE bridge_id=$1 AND login_id=$2 AND tool_kind=$3 AND server_label='' AND tool_name=$4 AND (action='' OR action=$5)
		LIMIT 1
	`, scope.bridgeID, scope.loginID, string(ToolApprovalKindBuiltin), toolName, action).Scan(&matched)
	if err == sql.ErrNoRows {
		return false
	}
	if err != nil {
		oc.Log().Warn().Err(err).Str("tool_name", toolName).Str("action", action).Msg("tool approvals: builtin lookup failed")
		return false
	}
	return matched == 1
}

func (oc *AIClient) insertToolApprovalRule(ctx context.Context, toolKind ToolApprovalKind, serverLabel, toolName, action string) error {
	scope := loginScopeForClient(oc)
	if scope == nil {
		return nil
	}
	_, err := scope.db.Exec(ctx, `
		INSERT INTO aichats_tool_approval_rules (
			bridge_id, login_id, tool_kind, server_label, tool_name, action, created_at_ms
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (bridge_id, login_id, tool_kind, server_label, tool_name, action) DO NOTHING
	`, scope.bridgeID, scope.loginID, string(toolKind), serverLabel, toolName, action, time.Now().UnixMilli())
	return err
}

func buildBuiltinApprovalPresentation(toolName, action string, args map[string]any) sdk.ApprovalPromptPresentation {
	toolName = strings.TrimSpace(toolName)
	details := make([]sdk.ApprovalDetail, 0, 10)
	if toolName != "" {
		details = append(details, sdk.ApprovalDetail{Label: "Tool", Value: toolName})
	}
	if action = strings.TrimSpace(action); action != "" {
		details = append(details, sdk.ApprovalDetail{Label: "Action", Value: action})
	}
	details = sdk.AppendDetailsFromMap(details, "Arg", args, 8)
	return sdk.BuildApprovalPresentation("Builtin tool request", toolName, details, true)
}

func buildMCPApprovalPresentation(serverLabel, toolName string, input any) sdk.ApprovalPromptPresentation {
	toolName = strings.TrimSpace(toolName)
	details := make([]sdk.ApprovalDetail, 0, 10)
	if serverLabel = strings.TrimSpace(serverLabel); serverLabel != "" {
		details = append(details, sdk.ApprovalDetail{Label: "Server", Value: serverLabel})
	}
	if toolName != "" {
		details = append(details, sdk.ApprovalDetail{Label: "Tool", Value: toolName})
	}
	if inputMap, ok := input.(map[string]any); ok && len(inputMap) > 0 {
		details = sdk.AppendDetailsFromMap(details, "Input", inputMap, 8)
	} else if summary := sdk.ValueSummary(input); summary != "" {
		details = append(details, sdk.ApprovalDetail{Label: "Input", Value: summary})
	}
	return sdk.BuildApprovalPresentation("MCP tool request", toolName, details, true)
}

func (oc *AIClient) builtinToolApprovalRequirement(toolName string, args map[string]any) (required bool, action string) {
	if oc == nil || !oc.toolApprovalsRuntimeEnabled() {
		return false, ""
	}
	toolName = strings.TrimSpace(toolName)
	if toolName == "" || !oc.toolApprovalsRequireForTool(toolName) {
		return false, ""
	}
	switch toolName {
	case ToolNameMessage:
		action = normalizeMessageAction(maputil.StringArg(args, "action"))
		switch action {
		// Read-only / non-destructive actions (do not require approval).
		case "search",
			// Desktop API read-only surface (AI Chats message tool actions).
			"desktop-list-chats", "desktop-search-chats", "desktop-search-messages", "desktop-download-asset":
			return false, action
		default:
			return true, action
		}
	default:
		if handled, required, action := oc.integratedToolApprovalRequirement(toolName, args); handled {
			return required, action
		}
		switch toolName {
		case ToolNameWrite, ToolNameEdit, ToolNameApplyPatch:
			return true, "workspace"
		}
		return true, ""
	}
}

type aiTurnApprovalHandle struct {
	client     *AIClient
	turn       *sdk.Turn
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

func (h *aiTurnApprovalHandle) Wait(ctx context.Context) (sdk.ToolApprovalResponse, error) {
	if h == nil || h.client == nil {
		return sdk.ToolApprovalResponse{}, nil
	}
	return sdk.WaitToolApprovalHandle(ctx, sdk.WaitToolApprovalHandleParams{
		Turn:             h.turn,
		ApprovalID:       h.approvalID,
		ToolCallID:       h.toolCallID,
		DenyToolOnReject: true,
	}, func(ctx context.Context) (sdk.ToolApprovalResponse, error) {
		resp, _, ok := h.client.waitToolApproval(ctx, h.approvalID)
		if !ok && resp.Reason == "" {
			resp.Reason = sdk.ApprovalWaitReason(ctx)
		}
		return resp, nil
	})
}

func newAITurnApprovalHandle(client *AIClient, turn *sdk.Turn, approvalID, toolCallID string) *aiTurnApprovalHandle {
	return &aiTurnApprovalHandle{
		client:     client,
		turn:       turn,
		approvalID: strings.TrimSpace(approvalID),
		toolCallID: strings.TrimSpace(toolCallID),
	}
}

func (oc *AIClient) approvalParamsFromRequest(portal *bridgev2.Portal, state *streamingState, turn *sdk.Turn, req sdk.ApprovalRequest) ToolApprovalParams {
	defaultTTL := sdk.DefaultApprovalExpiry
	if oc != nil {
		if ttl := time.Duration(oc.toolApprovalsTTLSeconds()) * time.Second; ttl > 0 {
			defaultTTL = ttl
		}
	}
	approvalID, ttl, presentation := sdk.ResolveApprovalRequest(req, NewCallID, defaultTTL, true)
	params := ToolApprovalParams{
		ApprovalID:   approvalID,
		ToolCallID:   strings.TrimSpace(req.ToolCallID),
		ToolName:     strings.TrimSpace(req.ToolName),
		Presentation: presentation,
		TTL:          ttl,
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
	turn *sdk.Turn,
	params ToolApprovalParams,
	sendPrompt bool,
) (sdk.ApprovalHandle, bool) {
	handle := newAITurnApprovalHandle(oc, turn, params.ApprovalID, params.ToolCallID)
	if oc == nil {
		return handle, false
	}
	ownerMXID := id.UserID("")
	if oc.UserLogin != nil {
		ownerMXID = oc.UserLogin.UserMXID
	}
	turnID, replyTo, threadRoot := resolveApprovalPromptContext(state, turn, params.TurnID)
	started := oc.approvalFlow.StartApprovalRequest(ctx, sdk.StartApprovalRequestParams[*pendingToolApprovalData]{
		Portal:             portal,
		OwnerMXID:          ownerMXID,
		SendPrompt:         sendPrompt,
		Request:            sdk.ApprovalRequest{ApprovalID: params.ApprovalID, ToolCallID: params.ToolCallID, ToolName: params.ToolName, TTL: params.TTL, Presentation: &params.Presentation},
		DefaultTTL:         params.TTL,
		DefaultAllowAlways: true,
		PromptContext: sdk.ApprovalPromptContext{
			TurnID:            turnID,
			ReplyToEventID:    replyTo,
			ThreadRootEventID: threadRoot,
		},
		EmitRequest: func(ctx context.Context, approvalID, toolCallID string) {
			if turn != nil {
				turn.Approvals().EmitRequest(ctx, approvalID, toolCallID)
			}
		},
		Data: &pendingToolApprovalData{
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
		},
	})
	if !started.Created {
		return handle, false
	}
	if !sendPrompt {
		return handle, true
	}
	if !started.PromptSent {
		_ = oc.resolveToolApproval(params.ApprovalID, false, sdk.ApprovalReasonDeliveryError)
		return handle, true
	}
	return handle, true
}

func (oc *AIClient) requestTurnApproval(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	turn *sdk.Turn,
	req sdk.ApprovalRequest,
) sdk.ApprovalHandle {
	if oc == nil {
		return newAITurnApprovalHandle(nil, nil, req.ApprovalID, req.ToolCallID)
	}
	params := oc.approvalParamsFromRequest(portal, state, turn, req)
	handle, _ := oc.startTurnApproval(ctx, portal, state, turn, params, true)
	return handle
}

func (oc *AIClient) registerToolApproval(params ToolApprovalParams) (*sdk.Pending[*pendingToolApprovalData], bool) {
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
	return oc.approvalFlow.Resolve(approvalID, sdk.ApprovalDecisionPayload{
		ApprovalID: approvalID,
		Approved:   approved,
		Reason:     strings.TrimSpace(reason),
	})
}

func (oc *AIClient) waitToolApproval(ctx context.Context, approvalID string) (sdk.ToolApprovalResponse, *pendingToolApprovalData, bool) {
	if oc == nil || oc.approvalFlow == nil {
		return sdk.ToolApprovalResponse{}, nil, false
	}
	approvalID = strings.TrimSpace(approvalID)
	if approvalID == "" {
		return sdk.ToolApprovalResponse{}, nil, false
	}

	p := oc.approvalFlow.Get(approvalID)
	if p == nil {
		return sdk.ToolApprovalResponse{}, nil, false
	}
	d := p.Data

	oc.Log().Debug().Str("approval_id", approvalID).Str("tool", d.ToolName).Msg("tool approval wait started")

	decision, d, ok := oc.approvalFlow.WaitAndFinalizeApproval(ctx, approvalID, sdk.WaitApprovalParams[*pendingToolApprovalData]{
		BuildNoDecision: func(reason string, _ *pendingToolApprovalData) *sdk.ApprovalDecisionPayload {
			if reason != sdk.ApprovalReasonTimeout {
				return nil
			}
			return &sdk.ApprovalDecisionPayload{
				ApprovalID: approvalID,
				Reason:     reason,
			}
		},
		OnResolved: func(ctx context.Context, decision sdk.ApprovalDecisionPayload, pending *pendingToolApprovalData) {
			state := "denied"
			if decision.Approved {
				state = "approved"
			}
			oc.Log().Debug().Str("approval_id", approvalID).Str("tool", pending.ToolName).Str("state", state).Msg("tool approval decision received")
			if decision.Approved && decision.Always {
				if err := oc.persistAlwaysAllow(ctx, pending); err != nil {
					oc.Log().Warn().Err(err).Str("approval_id", approvalID).Msg("Failed to persist always-allow rule")
				}
			}
		},
	})
	if !ok {
		reason := sdk.ApprovalWaitReason(ctx)
		if decision.Reason != "" {
			reason = decision.Reason
		}
		oc.Log().Debug().Str("approval_id", approvalID).Str("tool", d.ToolName).Str("reason", reason).Msg("tool approval wait ended without decision")
		return sdk.ToolApprovalResponse{Reason: reason}, d, false
	}

	return sdk.ToolApprovalResponse{
		Approved: decision.Approved,
		Always:   decision.Always,
		Reason:   decision.Reason,
	}, d, true
}

func (oc *AIClient) waitForToolApprovalResponse(
	ctx context.Context,
	handle sdk.ApprovalHandle,
) sdk.ToolApprovalResponse {
	touchAgentLoopActivity(ctx)
	if handle == nil {
		return sdk.ToolApprovalResponse{Reason: sdk.ApprovalWaitReason(ctx)}
	}
	resp, err := handle.Wait(ctx)
	touchAgentLoopActivity(ctx)
	if err != nil {
		return sdk.ToolApprovalResponse{Reason: err.Error()}
	}
	resp.Reason = strings.TrimSpace(resp.Reason)
	if !resp.Approved && resp.Reason == "" {
		resp.Reason = sdk.ApprovalReasonTimeout
	}
	return resp
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
	if required && oc.isBuiltinAlwaysAllowed(ctx, toolName, action) {
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
	handle := state.turn.Approvals().Request(sdk.ApprovalRequest{
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
	resp := oc.waitForToolApprovalResponse(ctx, handle)
	return !resp.Approved
}
