package connector

import (
	"context"
	"fmt"
	"strings"
	"time"

	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

type ToolApprovalKind string

const (
	ToolApprovalKindMCP     ToolApprovalKind = "mcp"
	ToolApprovalKindBuiltin ToolApprovalKind = "builtin"
)

// ToolApprovalDecision is a user decision for a pending tool approval request.
type ToolApprovalDecision struct {
	Approve   bool
	Always    bool   // Persist allow rule when true (only meaningful when Approve=true).
	Reason    string // Optional; forwarded upstream when supported.
	DecidedAt time.Time
	DecidedBy id.UserID
}

type pendingToolApproval struct {
	ApprovalID string
	RoomID     id.RoomID
	TurnID     string

	ToolCallID string
	ToolName   string // display name (e.g. "message" or "mcp.<name>")

	ToolKind     ToolApprovalKind
	RuleToolName string // normalized for matching/persistence (e.g. "message" or raw MCP tool name without "mcp.")
	ServerLabel  string // MCP only
	Action       string // builtin only (optional)

	TargetEventID id.EventID // Matrix event ID to react to (tool call timeline event)
	// ApprovalEventID tracks the timeline message used for durable approval UI (for edits).
	ApprovalEventID id.EventID
	// ApprovalEventUseBot records whether the approval message was sent as the bridge bot.
	ApprovalEventUseBot bool

	RequestedAt time.Time
	ExpiresAt   time.Time

	decisionCh chan ToolApprovalDecision
}

func (oc *AIClient) registerToolApproval(params struct {
	ApprovalID string
	RoomID     id.RoomID
	TurnID     string

	ToolCallID string
	ToolName   string

	ToolKind     ToolApprovalKind
	RuleToolName string
	ServerLabel  string
	Action       string
	TargetEvent  id.EventID

	TTL time.Duration
}) (*pendingToolApproval, bool) {
	if oc == nil {
		return nil, false
	}
	approvalID := strings.TrimSpace(params.ApprovalID)
	if approvalID == "" {
		return nil, false
	}
	ttl := params.TTL
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}

	oc.toolApprovalsMu.Lock()
	defer oc.toolApprovalsMu.Unlock()

	if existing := oc.toolApprovals[approvalID]; existing != nil {
		return existing, false
	}

	now := time.Now()
	p := &pendingToolApproval{
		ApprovalID:    approvalID,
		RoomID:        params.RoomID,
		TurnID:        params.TurnID,
		ToolCallID:    strings.TrimSpace(params.ToolCallID),
		ToolName:      strings.TrimSpace(params.ToolName),
		ToolKind:      params.ToolKind,
		RuleToolName:  strings.TrimSpace(params.RuleToolName),
		ServerLabel:   strings.TrimSpace(params.ServerLabel),
		Action:        strings.TrimSpace(params.Action),
		TargetEventID: params.TargetEvent,
		RequestedAt:   now,
		ExpiresAt:     now.Add(ttl),
		decisionCh:    make(chan ToolApprovalDecision, 1),
	}
	oc.toolApprovals[approvalID] = p
	if p.TargetEventID != "" {
		// Best-effort: later approvals for the same target message should not override.
		if _, exists := oc.toolApprovalsByTargetEvt[p.TargetEventID]; !exists {
			oc.toolApprovalsByTargetEvt[p.TargetEventID] = approvalID
		}
	}
	return p, true
}

func (oc *AIClient) setApprovalSnapshotEvent(approvalID string, eventID id.EventID, useBot bool) {
	if oc == nil {
		return
	}
	approvalID = strings.TrimSpace(approvalID)
	if approvalID == "" || eventID == "" {
		return
	}
	oc.toolApprovalsMu.Lock()
	if p := oc.toolApprovals[approvalID]; p != nil {
		p.ApprovalEventID = eventID
		p.ApprovalEventUseBot = useBot
	}
	oc.toolApprovalsMu.Unlock()
}

func (oc *AIClient) resolveToolApproval(roomID id.RoomID, approvalID string, decision ToolApprovalDecision) error {
	if oc == nil || oc.UserLogin == nil {
		return fmt.Errorf("bridge not available")
	}
	approvalID = strings.TrimSpace(approvalID)
	if approvalID == "" {
		return fmt.Errorf("missing approval id")
	}
	if strings.TrimSpace(roomID.String()) == "" {
		return fmt.Errorf("missing room id")
	}
	if decision.DecidedBy == "" || decision.DecidedBy != oc.UserLogin.UserMXID {
		return fmt.Errorf("only the owner can approve")
	}

	oc.toolApprovalsMu.Lock()
	p := oc.toolApprovals[approvalID]
	oc.toolApprovalsMu.Unlock()
	if p == nil {
		return fmt.Errorf("unknown or expired approval id: %s", approvalID)
	}
	if p.RoomID != roomID {
		return fmt.Errorf("approval id does not belong to this room")
	}
	if time.Now().After(p.ExpiresAt) {
		oc.dropToolApprovalLocked(approvalID)
		return fmt.Errorf("approval expired: %s", approvalID)
	}

	decision.Reason = strings.TrimSpace(decision.Reason)
	if decision.DecidedAt.IsZero() {
		decision.DecidedAt = time.Now()
	}
	select {
	case p.decisionCh <- decision:
		go oc.emitApprovalSnapshotDecision(approvalID, decision)
		return nil
	default:
		return fmt.Errorf("approval already resolved: %s", approvalID)
	}
}

func (oc *AIClient) resolveToolApprovalByTargetEvent(roomID id.RoomID, targetEventID id.EventID, decision ToolApprovalDecision) error {
	if oc == nil {
		return fmt.Errorf("bridge not available")
	}
	targetEventID = id.EventID(strings.TrimSpace(string(targetEventID)))
	if targetEventID == "" {
		return fmt.Errorf("missing target event id")
	}
	oc.toolApprovalsMu.Lock()
	approvalID := oc.toolApprovalsByTargetEvt[targetEventID]
	oc.toolApprovalsMu.Unlock()
	if strings.TrimSpace(approvalID) == "" {
		return fmt.Errorf("no pending approval for that message")
	}
	return oc.resolveToolApproval(roomID, approvalID, decision)
}

func (oc *AIClient) waitToolApproval(ctx context.Context, approvalID string) (ToolApprovalDecision, *pendingToolApproval, bool) {
	if oc == nil {
		return ToolApprovalDecision{}, nil, false
	}
	approvalID = strings.TrimSpace(approvalID)
	if approvalID == "" {
		return ToolApprovalDecision{}, nil, false
	}

	oc.toolApprovalsMu.Lock()
	p := oc.toolApprovals[approvalID]
	oc.toolApprovalsMu.Unlock()
	if p == nil {
		return ToolApprovalDecision{}, nil, false
	}

	timeout := time.Until(p.ExpiresAt)
	if timeout <= 0 {
		oc.dropToolApprovalLocked(approvalID)
		return ToolApprovalDecision{}, p, false
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case decision := <-p.decisionCh:
		if decision.Approve && decision.Always {
			_ = oc.persistAlwaysAllow(ctx, p)
		}
		oc.dropToolApprovalLocked(approvalID)
		return decision, p, true
	case <-timer.C:
		oc.dropToolApprovalLocked(approvalID)
		return ToolApprovalDecision{}, p, false
	case <-ctx.Done():
		oc.dropToolApprovalLocked(approvalID)
		return ToolApprovalDecision{}, p, false
	}
}

func (oc *AIClient) emitApprovalSnapshotDecision(approvalID string, decision ToolApprovalDecision) {
	if oc == nil || oc.UserLogin == nil {
		return
	}
	approvalID = strings.TrimSpace(approvalID)
	if approvalID == "" {
		return
	}
	oc.toolApprovalsMu.Lock()
	p := oc.toolApprovals[approvalID]
	oc.toolApprovalsMu.Unlock()
	if p == nil || p.ApprovalEventID == "" {
		return
	}

	ctx := oc.backgroundContext(context.Background())
	portal, err := oc.UserLogin.Bridge.GetPortalByMXID(ctx, p.RoomID)
	if err != nil || portal == nil || portal.MXID == "" {
		return
	}

	toolName := strings.TrimSpace(p.ToolName)
	if toolName == "" {
		toolName = "tool"
	}

	state := "output-denied"
	body := fmt.Sprintf("Approval denied for %s.", toolName)
	toolPart := map[string]any{
		"type":       "dynamic-tool",
		"toolName":   toolName,
		"toolCallId": p.ToolCallID,
		"state":      state,
	}
	if decision.Approve {
		state = "output-available"
		body = fmt.Sprintf("Approved %s.", toolName)
		toolPart["state"] = state
		toolPart["output"] = map[string]any{"message": "Approved"}
	} else {
		reason := strings.TrimSpace(decision.Reason)
		if reason == "" {
			reason = "Denied"
		}
		toolPart["errorText"] = reason
	}

	uiMessage := map[string]any{
		"id":       "approval:" + approvalID,
		"role":     "assistant",
		"metadata": map[string]any{"turn_id": p.TurnID},
		"parts":    []map[string]any{toolPart},
	}

	eventRaw := map[string]any{
		"msgtype": event.MsgNotice,
		"body":    "* " + body,
		"m.new_content": map[string]any{
			"msgtype": event.MsgNotice,
			"body":    body,
		},
		"m.relates_to": map[string]any{
			"rel_type": RelReplace,
			"event_id": p.ApprovalEventID.String(),
		},
		BeeperAIKey:                     uiMessage,
		"com.beeper.dont_render_edited": true,
	}
	eventContent := &event.Content{Raw: eventRaw}

	sendWithBot := p.ApprovalEventUseBot
	if !sendWithBot {
		if intent := oc.getModelIntent(ctx, portal); intent != nil {
			if _, err := intent.SendMessage(ctx, portal.MXID, event.EventMessage, eventContent, nil); err == nil {
				return
			}
		}
	}
	if oc.UserLogin.Bridge != nil && oc.UserLogin.Bridge.Bot != nil {
		_, _ = oc.UserLogin.Bridge.Bot.SendMessage(ctx, portal.MXID, event.EventMessage, eventContent, nil)
	}
}

func (oc *AIClient) dropToolApprovalLocked(approvalID string) {
	if oc == nil {
		return
	}
	approvalID = strings.TrimSpace(approvalID)
	if approvalID == "" {
		return
	}
	oc.toolApprovalsMu.Lock()
	delete(oc.toolApprovals, approvalID)
	// Clean up any target-event mappings pointing at this approval (tool call event, fallback notice, etc).
	// This keeps behavior correct even if multiple event IDs were mapped to the same approval.
	for evtID, mapped := range oc.toolApprovalsByTargetEvt {
		if mapped == approvalID {
			delete(oc.toolApprovalsByTargetEvt, evtID)
		}
	}
	oc.toolApprovalsMu.Unlock()
}

// addToolApprovalTargetEvent maps an additional Matrix event ID to an existing pending approval.
// This enables reaction-based approvals on fallback timeline notices (and other correlated events).
func (oc *AIClient) addToolApprovalTargetEvent(approvalID string, targetEventID id.EventID) {
	if oc == nil {
		return
	}
	approvalID = strings.TrimSpace(approvalID)
	targetEventID = id.EventID(strings.TrimSpace(string(targetEventID)))
	if approvalID == "" || targetEventID == "" {
		return
	}

	oc.toolApprovalsMu.Lock()
	defer oc.toolApprovalsMu.Unlock()

	if oc.toolApprovals[approvalID] == nil {
		return
	}
	// Best-effort: do not override an existing mapping for this target message.
	if _, exists := oc.toolApprovalsByTargetEvt[targetEventID]; !exists {
		oc.toolApprovalsByTargetEvt[targetEventID] = approvalID
	}
}
