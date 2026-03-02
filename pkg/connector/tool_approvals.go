package connector

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/ai-bridge/pkg/bridgeadapter"
	airuntime "github.com/beeper/ai-bridge/pkg/runtime"
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
// ApprovalManager's PendingApproval.Data field.
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
	// ApprovalEventID tracks the Matrix event ID for reverse-lookup by findApprovalByEventID.
	ApprovalEventID id.EventID
	// ApprovalNetworkMsgID is the network message ID returned by sendViaPortal, used for edits.
	ApprovalNetworkMsgID networkid.MessageID

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

	TTL time.Duration
}

func (oc *AIClient) registerToolApproval(params ToolApprovalParams) (*bridgeadapter.PendingApproval[toolApprovalResolution], bool) {
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
		RequestedAt:  time.Now(),
	}
	p, created := oc.approvals.Register(params.ApprovalID, params.TTL, data)
	if created {
		oc.Log().Debug().Str("approval_id", params.ApprovalID).Str("tool", params.ToolName).Dur("ttl", params.TTL).Msg("tool approval registered")
	}
	return p, created
}

func (oc *AIClient) resolveToolApproval(
	roomID id.RoomID,
	approvalID string,
	decision airuntime.ToolApprovalDecision,
	always bool,
	decidedBy id.UserID,
) error {
	if oc == nil || oc.UserLogin == nil {
		return errors.New("bridge not available")
	}
	approvalID = strings.TrimSpace(approvalID)
	if approvalID == "" {
		return bridgeadapter.ErrApprovalMissingID
	}
	if strings.TrimSpace(roomID.String()) == "" {
		return bridgeadapter.ErrApprovalMissingRoom
	}
	if decidedBy == "" || decidedBy != oc.UserLogin.UserMXID {
		return bridgeadapter.ErrApprovalOnlyOwner
	}

	p := oc.approvals.Get(approvalID)
	if p == nil {
		return fmt.Errorf("%w: %s", bridgeadapter.ErrApprovalUnknown, approvalID)
	}
	d := approvalData(p)
	if d.RoomID != roomID {
		return bridgeadapter.ErrApprovalWrongRoom
	}

	decision.Reason = strings.TrimSpace(decision.Reason)
	if strings.TrimSpace(string(decision.State)) == "" {
		decision.State = airuntime.ToolApprovalDenied
	}
	resolution := toolApprovalResolution{
		Decision: decision,
		Always:   always,
	}
	if err := oc.approvals.Resolve(approvalID, resolution); err != nil {
		return err
	}
	oc.Log().Debug().Str("approval_id", approvalID).Str("tool", d.ToolName).Str("state", string(decision.State)).Msg("tool approval decision delivered")
	go oc.emitApprovalSnapshotDecision(d, decision)
	return nil
}

func (oc *AIClient) waitToolApproval(ctx context.Context, approvalID string) (toolApprovalResolution, *pendingToolApprovalData, bool) {
	if oc == nil || oc.UserLogin == nil {
		return toolApprovalResolution{}, nil, false
	}
	approvalID = strings.TrimSpace(approvalID)
	if approvalID == "" {
		return toolApprovalResolution{}, nil, false
	}

	p := oc.approvals.Get(approvalID)
	if p == nil {
		return toolApprovalResolution{}, nil, false
	}
	d := approvalData(p)

	oc.Log().Debug().Str("approval_id", approvalID).Str("tool", d.ToolName).Msg("tool approval wait started")

	resolution, ok := oc.approvals.Wait(ctx, approvalID)
	if !ok {
		// Determine if it was a timeout or context cancellation for snapshot purposes.
		reason := "timeout"
		state := airuntime.ToolApprovalTimedOut
		if ctx.Err() != nil {
			reason = "cancelled"
			state = airuntime.ToolApprovalStale
		}
		oc.Log().Debug().Str("approval_id", approvalID).Str("tool", d.ToolName).Str("reason", reason).Msg("tool approval wait ended without decision")
		go oc.emitApprovalSnapshotDecision(d, airuntime.ToolApprovalDecision{
			State:  state,
			Reason: reason,
		})
		return toolApprovalResolution{}, d, false
	}

	oc.Log().Debug().Str("approval_id", approvalID).Str("tool", d.ToolName).Str("state", string(resolution.Decision.State)).Msg("tool approval decision received")
	if approvalAllowed(resolution.Decision) && resolution.Always {
		if err := oc.persistAlwaysAllow(ctx, d); err != nil {
			oc.Log().Warn().Err(err).Str("approval_id", approvalID).Msg("Failed to persist always-allow rule")
		}
	}
	return resolution, d, true
}

func approvalAllowed(decision airuntime.ToolApprovalDecision) bool {
	return decision.State == airuntime.ToolApprovalApproved
}

// approvalData extracts the pendingToolApprovalData from a PendingApproval.
func approvalData(p *bridgeadapter.PendingApproval[toolApprovalResolution]) *pendingToolApprovalData {
	if p == nil || p.Data == nil {
		return &pendingToolApprovalData{}
	}
	if d, ok := p.Data.(*pendingToolApprovalData); ok {
		return d
	}
	return &pendingToolApprovalData{}
}

func (oc *AIClient) emitApprovalSnapshotDecision(d *pendingToolApprovalData, decision airuntime.ToolApprovalDecision) {
	if oc == nil || oc.UserLogin == nil || d == nil {
		return
	}
	// ApprovalNetworkMsgID may be empty if the approval was resolved before the timeline
	// message was sent (race between resolveToolApproval and emitUIToolApprovalRequest).
	// This is harmless — the stream event path (tool-output-*) resolves the UI instead.
	if d.ApprovalNetworkMsgID == "" {
		return
	}

	ctx := oc.backgroundContext(context.Background())
	portal := oc.portalByRoomID(ctx, d.RoomID)
	if portal == nil || portal.MXID == "" {
		return
	}

	toolName := strings.TrimSpace(d.ToolName)
	if toolName == "" {
		toolName = "tool"
	}

	// Determine selection state for the action hints edit
	selectedHint := -1
	body := fmt.Sprintf("Approval denied for %s.", toolName)
	uiState := "output-denied"
	if approvalAllowed(decision) {
		selectedHint = 0 // "Allow" button
		body = fmt.Sprintf("Approved %s.", toolName)
		uiState = "output-available"
	} else {
		reason := strings.TrimSpace(decision.Reason)
		switch strings.ToLower(reason) {
		case "timeout", "expired", "cancelled", "canceled":
			body = fmt.Sprintf("Approval expired for %s.", toolName)
			selectedHint = -1 // no selection, just disable
		default:
			selectedHint = 2 // "Deny" button
		}
	}

	// Build updated action hints with selection state (disabled buttons)
	updatedHints := map[string]any{
		"hints":    []any{}, // empty — buttons are disabled after selection
		"resolved": true,
	}
	if selectedHint >= 0 {
		updatedHints["selected_hint"] = selectedHint
	}

	uiPart := map[string]any{
		"type":       "action-hints",
		"toolCallId": d.ToolCallID,
		"toolName":   toolName,
		"state":      uiState,
	}
	uiMessage := map[string]any{
		"id":       "approval:" + d.ApprovalID,
		"role":     "assistant",
		"metadata": map[string]any{"turn_id": d.TurnID},
		"parts":    []map[string]any{uiPart},
	}

	// Look up the DB message part so sendEditViaPortal can reference the original event.
	receiver := portal.Receiver
	if receiver == "" {
		receiver = oc.UserLogin.ID
	}
	parts, err := oc.UserLogin.Bridge.DB.Message.GetAllPartsByID(ctx, receiver, d.ApprovalNetworkMsgID)
	if err != nil || len(parts) == 0 {
		oc.Log().Warn().Err(err).Str("approval_id", d.ApprovalID).Msg("tool approval: approval message not found in DB for edit")
		return
	}

	editExtra := map[string]any{
		BeeperAIKey:                     uiMessage,
		BeeperActionHintsKey:            updatedHints,
		"com.beeper.dont_render_edited": true,
		"m.mentions":                    map[string]any{},
	}
	converted := &bridgev2.ConvertedEdit{
		ModifiedParts: []*bridgev2.ConvertedEditPart{{
			Part: parts[0],
			Type: event.EventMessage,
			Content: &event.MessageEventContent{
				MsgType: event.MsgNotice,
				Body:    body,
			},
			Extra:         map[string]any{BeeperActionHintsKey: updatedHints, "m.mentions": map[string]any{}},
			TopLevelExtra: editExtra,
		}},
	}
	if err := oc.sendEditViaPortal(ctx, portal, d.ApprovalNetworkMsgID, converted); err != nil {
		oc.Log().Warn().Err(err).Str("approval_id", d.ApprovalID).Msg("tool approval: failed to send snapshot decision")
	}
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
	if _, created := oc.registerToolApproval(ToolApprovalParams{
		ApprovalID:   approvalID,
		RoomID:       state.roomID,
		TurnID:       state.turnID,
		ToolCallID:   tool.callID,
		ToolName:     toolName,
		ToolKind:     ToolApprovalKindBuiltin,
		RuleToolName: toolName,
		Action:       action,
		TTL:          ttl,
	}); !created {
		oc.loggerForContext(ctx).Error().
			Str("tool_name", toolName).
			Msg("tool approval: failed to register builtin approval request")
		return true
	}
	oc.emitUIToolApprovalRequest(ctx, portal, state, approvalID, tool.callID, toolName, tool.eventID, oc.toolApprovalsTTLSeconds())
	resolution, _, ok := oc.waitToolApproval(ctx, approvalID)
	decision := resolution.Decision
	if !ok {
		if oc.toolApprovalsAskFallback() == "allow" {
			decision = airuntime.ToolApprovalDecision{State: airuntime.ToolApprovalApproved, Reason: "fallback"}
		} else {
			decision = airuntime.ToolApprovalDecision{State: airuntime.ToolApprovalTimedOut, Reason: "timeout"}
		}
	}
	if !approvalAllowed(decision) {
		oc.uiEmitter(state).EmitUIToolOutputDenied(ctx, portal, tool.callID)
		return true
	}
	return false
}

// findApprovalByEventID returns the approval ID for a pending approval whose
// timeline message matches the given Matrix event ID. Returns "" if not found.
func (oc *AIClient) findApprovalByEventID(eventID id.EventID) string {
	if oc == nil || eventID == "" {
		return ""
	}
	return oc.approvals.FindByData(func(data any) bool {
		if d, ok := data.(*pendingToolApprovalData); ok {
			return d.ApprovalEventID == eventID
		}
		return false
	})
}

func (oc *AIClient) dropToolApproval(approvalID string) {
	if oc == nil {
		return
	}
	oc.approvals.Drop(approvalID)
	oc.Log().Debug().Str("approval_id", approvalID).Msg("tool approval dropped")
}
