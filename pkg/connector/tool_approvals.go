package connector

import (
	"context"
	"errors"
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
		ApprovalID:   approvalID,
		RoomID:       params.RoomID,
		TurnID:       params.TurnID,
		ToolCallID:   strings.TrimSpace(params.ToolCallID),
		ToolName:     strings.TrimSpace(params.ToolName),
		ToolKind:     params.ToolKind,
		RuleToolName: strings.TrimSpace(params.RuleToolName),
		ServerLabel:  strings.TrimSpace(params.ServerLabel),
		Action:       strings.TrimSpace(params.Action),
		RequestedAt:  now,
		ExpiresAt:    now.Add(ttl),
		decisionCh:   make(chan ToolApprovalDecision, 1),
	}
	oc.toolApprovals[approvalID] = p
	oc.Log().Debug().Str("approval_id", approvalID).Str("tool", params.ToolName).Dur("ttl", ttl).Msg("tool approval registered")
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
		return errors.New("bridge not available")
	}
	approvalID = strings.TrimSpace(approvalID)
	if approvalID == "" {
		return ErrApprovalMissingID
	}
	if strings.TrimSpace(roomID.String()) == "" {
		return ErrApprovalMissingRoom
	}
	if decision.DecidedBy == "" || decision.DecidedBy != oc.UserLogin.UserMXID {
		return ErrApprovalOnlyOwner
	}

	oc.toolApprovalsMu.Lock()
	p := oc.toolApprovals[approvalID]
	oc.toolApprovalsMu.Unlock()
	if p == nil {
		return fmt.Errorf("%w: %s", ErrApprovalUnknown, approvalID)
	}
	if p.RoomID != roomID {
		return ErrApprovalWrongRoom
	}
	if time.Now().After(p.ExpiresAt) {
		oc.dropToolApproval(approvalID)
		return fmt.Errorf("%w: %s", ErrApprovalExpired, approvalID)
	}

	decision.Reason = strings.TrimSpace(decision.Reason)
	if decision.DecidedAt.IsZero() {
		decision.DecidedAt = time.Now()
	}
	select {
	case p.decisionCh <- decision:
		oc.Log().Debug().Str("approval_id", approvalID).Str("tool", p.ToolName).Bool("approved", decision.Approve).Msg("tool approval decision delivered")
		go oc.emitApprovalSnapshotDecision(p, decision)
		return nil
	default:
		oc.dropToolApproval(approvalID)
		return fmt.Errorf("%w: %s", ErrApprovalAlreadyHandled, approvalID)
	}
}

func (oc *AIClient) waitToolApproval(ctx context.Context, approvalID string) (ToolApprovalDecision, *pendingToolApproval, bool) {
	if oc == nil || oc.UserLogin == nil {
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

	oc.Log().Debug().Str("approval_id", approvalID).Str("tool", p.ToolName).Msg("tool approval wait started")

	timeout := time.Until(p.ExpiresAt)
	if timeout <= 0 {
		oc.Log().Debug().Str("approval_id", approvalID).Str("tool", p.ToolName).Msg("tool approval already expired")
		oc.dropToolApproval(approvalID)
		// Best-effort snapshot update so clients stop showing approval UI.
		// Pass p directly — the map entry is already dropped, but the pointer is still valid.
		go oc.emitApprovalSnapshotDecision(p, ToolApprovalDecision{
			Approve:   false,
			Reason:    "expired",
			DecidedAt: time.Now(),
			DecidedBy: oc.UserLogin.UserMXID,
		})
		return ToolApprovalDecision{}, p, false
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case decision := <-p.decisionCh:
		oc.Log().Debug().Str("approval_id", approvalID).Str("tool", p.ToolName).Bool("approved", decision.Approve).Msg("tool approval decision received")
		if decision.Approve && decision.Always {
			if err := oc.persistAlwaysAllow(ctx, p); err != nil {
				oc.Log().Warn().Err(err).Str("approval_id", approvalID).Msg("Failed to persist always-allow rule")
			}
		}
		oc.dropToolApproval(approvalID)
		return decision, p, true
	case <-timer.C:
		oc.Log().Debug().Str("approval_id", approvalID).Str("tool", p.ToolName).Msg("tool approval timed out")
		oc.dropToolApproval(approvalID)
		// Timeout: update the approval snapshot so the UI can stop showing action buttons,
		// even if the tool is no longer waiting (e.g. on reconnect).
		go oc.emitApprovalSnapshotDecision(p, ToolApprovalDecision{
			Approve:   false,
			Reason:    "timeout",
			DecidedAt: time.Now(),
			DecidedBy: oc.UserLogin.UserMXID,
		})
		return ToolApprovalDecision{}, p, false
	case <-ctx.Done():
		oc.Log().Debug().Str("approval_id", approvalID).Str("tool", p.ToolName).Msg("tool approval context cancelled")
		oc.dropToolApproval(approvalID)
		// Context cancellation: treat as expired for UI purposes.
		go oc.emitApprovalSnapshotDecision(p, ToolApprovalDecision{
			Approve:   false,
			Reason:    "cancelled",
			DecidedAt: time.Now(),
			DecidedBy: oc.UserLogin.UserMXID,
		})
		return ToolApprovalDecision{}, p, false
	}
}

func (oc *AIClient) emitApprovalSnapshotDecision(p *pendingToolApproval, decision ToolApprovalDecision) {
	if oc == nil || oc.UserLogin == nil || p == nil {
		return
	}
	// ApprovalEventID may be empty if the approval was resolved before the timeline
	// message was sent (race between resolveToolApproval and emitUIToolApprovalRequest).
	// This is harmless — the stream event path (tool-output-*) resolves the UI instead.
	if p.ApprovalEventID == "" {
		return
	}

	ctx := oc.backgroundContext(context.Background())
	portal := oc.portalByRoomID(ctx, p.RoomID)
	if portal == nil || portal.MXID == "" {
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
		switch strings.ToLower(reason) {
		case "timeout", "expired", "cancelled", "canceled":
			body = fmt.Sprintf("Approval expired for %s.", toolName)
			toolPart["errorText"] = "Expired"
		default:
			if reason == "" {
				reason = "Denied"
			}
			toolPart["errorText"] = reason
		}
	}

	uiMessage := map[string]any{
		"id":       "approval:" + p.ApprovalID,
		"role":     "assistant",
		"metadata": map[string]any{"turn_id": p.TurnID},
		"parts":    []map[string]any{toolPart},
	}

	eventRaw := map[string]any{
		"msgtype": event.MsgNotice,
		"body":    "* " + body,
		"m.new_content": map[string]any{
			"msgtype":    event.MsgNotice,
			"body":       body,
			"m.mentions": map[string]any{},
		},
		"m.relates_to": map[string]any{
			"rel_type": RelReplace,
			"event_id": p.ApprovalEventID.String(),
		},
		BeeperAIKey:                     uiMessage,
		"com.beeper.dont_render_edited": true,
		"m.mentions":                    map[string]any{},
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
		if _, err := oc.UserLogin.Bridge.Bot.SendMessage(ctx, portal.MXID, event.EventMessage, eventContent, nil); err != nil {
			oc.Log().Warn().Err(err).Str("approval_id", p.ApprovalID).Msg("tool approval: failed to send snapshot decision via bot")
		}
	}
}

func (oc *AIClient) dropToolApproval(approvalID string) {
	if oc == nil {
		return
	}
	approvalID = strings.TrimSpace(approvalID)
	if approvalID == "" {
		return
	}
	oc.toolApprovalsMu.Lock()
	delete(oc.toolApprovals, approvalID)
	oc.toolApprovalsMu.Unlock()
	oc.Log().Debug().Str("approval_id", approvalID).Msg("tool approval dropped")
}
