package ai

import (
	"context"
	"fmt"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/id"
)

type stopPlanKind string

const (
	stopPlanKindNoMatch  stopPlanKind = "no-match"
	stopPlanKindRoomWide stopPlanKind = "room-wide"
	stopPlanKindActive   stopPlanKind = "active-turn"
	stopPlanKindQueued   stopPlanKind = "queued-turn"
)

type userStopRequest struct {
	Portal             *bridgev2.Portal
	Meta               *PortalMetadata
	ReplyTo            id.EventID
	RequestedByEventID id.EventID
	RequestedVia       string
}

type userStopPlan struct {
	Kind          stopPlanKind
	Scope         string
	TargetKind    string
	TargetEventID id.EventID
}

type userStopResult struct {
	Plan             userStopPlan
	ActiveStopped    bool
	QueuedStopped    int
	SubagentsStopped int
}

func stopLabel(count int, singular string) string {
	if count == 1 {
		return singular
	}
	return singular + "s"
}

func formatAbortNotice(result userStopResult) string {
	switch result.Plan.Kind {
	case stopPlanKindNoMatch:
		return "No matching active or queued turn found for that reply."
	case stopPlanKindActive:
		if result.SubagentsStopped > 0 {
			return fmt.Sprintf("Stopped that turn. Stopped %d %s.", result.SubagentsStopped, stopLabel(result.SubagentsStopped, "sub-agent"))
		}
		return "Stopped that turn."
	case stopPlanKindQueued:
		if result.QueuedStopped <= 1 {
			return "Stopped that queued turn."
		}
		return fmt.Sprintf("Stopped %d queued %s.", result.QueuedStopped, stopLabel(result.QueuedStopped, "turn"))
	case stopPlanKindRoomWide:
		parts := make([]string, 0, 3)
		if result.ActiveStopped {
			parts = append(parts, "stopped the active turn")
		}
		if result.QueuedStopped > 0 {
			parts = append(parts, fmt.Sprintf("removed %d queued %s", result.QueuedStopped, stopLabel(result.QueuedStopped, "turn")))
		}
		if result.SubagentsStopped > 0 {
			parts = append(parts, fmt.Sprintf("stopped %d %s", result.SubagentsStopped, stopLabel(result.SubagentsStopped, "sub-agent")))
		}
		if len(parts) == 0 {
			return "No active or queued turns to stop."
		}
		suffix := ""
		if len(parts) > 1 {
			suffix = " " + strings.Join(parts[1:], ". ") + "."
		}
		return strings.ToUpper(parts[0][:1]) + parts[0][1:] + "." + suffix
	default:
		return "No active or queued turns to stop."
	}
}

func (oc *AIClient) pendingQueueHasSourceEvent(roomID id.RoomID, sourceEventID id.EventID) bool {
	if oc == nil || roomID == "" || sourceEventID == "" {
		return false
	}
	queue := oc.getQueueSnapshot(roomID)
	if queue == nil {
		return false
	}
	for _, item := range queue.items {
		if item.pending.sourceEventID() == sourceEventID {
			return true
		}
	}
	return false
}

func buildStopMetadata(plan userStopPlan, req userStopRequest) *assistantStopMetadata {
	return &assistantStopMetadata{
		Reason:             "user_stop",
		Scope:              plan.Scope,
		TargetKind:         plan.TargetKind,
		TargetEventID:      plan.TargetEventID.String(),
		RequestedByEventID: req.RequestedByEventID.String(),
		RequestedVia:       strings.TrimSpace(req.RequestedVia),
	}
}

func (oc *AIClient) resolveUserStopPlan(req userStopRequest) userStopPlan {
	if req.Portal == nil || req.Portal.MXID == "" {
		return userStopPlan{Kind: stopPlanKindNoMatch}
	}
	if req.ReplyTo == "" {
		return userStopPlan{
			Kind:       stopPlanKindRoomWide,
			Scope:      "room",
			TargetKind: "all",
		}
	}

	_, sourceEventID, initialEventID, _ := oc.roomRunTarget(req.Portal.MXID)
	if initialEventID != "" && req.ReplyTo == initialEventID {
		return userStopPlan{
			Kind:          stopPlanKindActive,
			Scope:         "turn",
			TargetKind:    "placeholder_event",
			TargetEventID: req.ReplyTo,
		}
	}
	if sourceEventID != "" && req.ReplyTo == sourceEventID {
		return userStopPlan{
			Kind:          stopPlanKindActive,
			Scope:         "turn",
			TargetKind:    "source_event",
			TargetEventID: req.ReplyTo,
		}
	}
	if oc.pendingQueueHasSourceEvent(req.Portal.MXID, req.ReplyTo) {
		return userStopPlan{
			Kind:          stopPlanKindQueued,
			Scope:         "turn",
			TargetKind:    "source_event",
			TargetEventID: req.ReplyTo,
		}
	}
	return userStopPlan{
		Kind:          stopPlanKindNoMatch,
		Scope:         "turn",
		TargetEventID: req.ReplyTo,
	}
}

func (oc *AIClient) finalizeStoppedQueueItems(ctx context.Context, items []pendingQueueItem) int {
	for _, item := range items {
		if item.pending.Meta != nil && item.pending.Meta.AckReactionRemoveAfter {
			oc.removePendingAckReactions(oc.backgroundContext(ctx), item.pending.Portal, item.pending)
		}
		oc.sendQueueRejectedStatus(ctx, item.pending.Portal, item.pending.Event, item.pending.StatusEvents, "Stopped.")
	}
	return len(items)
}

func (oc *AIClient) executeUserStopPlan(ctx context.Context, req userStopRequest, plan userStopPlan) userStopResult {
	result := userStopResult{Plan: plan}
	if req.Portal == nil || req.Portal.MXID == "" {
		return result
	}
	roomID := req.Portal.MXID
	switch plan.Kind {
	case stopPlanKindRoomWide:
		if oc.markRoomRunStopped(roomID, buildStopMetadata(plan, req)) {
			result.ActiveStopped = oc.cancelRoomRun(roomID)
		}
		result.QueuedStopped = oc.finalizeStoppedQueueItems(ctx, oc.drainPendingQueue(roomID))
		result.SubagentsStopped = oc.stopSubagentRuns(roomID)
	case stopPlanKindActive:
		if oc.markRoomRunStopped(roomID, buildStopMetadata(plan, req)) {
			result.ActiveStopped = oc.cancelRoomRun(roomID)
			if result.ActiveStopped {
				result.SubagentsStopped = oc.stopSubagentRuns(roomID)
			}
		}
	case stopPlanKindQueued:
		result.QueuedStopped = oc.finalizeStoppedQueueItems(ctx, oc.removePendingQueueBySourceEvent(roomID, plan.TargetEventID))
	}

	if req.Meta != nil && (result.ActiveStopped || result.QueuedStopped > 0 || result.SubagentsStopped > 0) {
		req.Meta.AbortedLastRun = true
		oc.savePortalQuiet(ctx, req.Portal, "stop")
	}
	if req.Meta != nil && result.QueuedStopped > 0 {
		oc.notifySessionMutation(ctx, req.Portal, req.Meta, false)
	}
	return result
}

func (oc *AIClient) handleUserStop(ctx context.Context, req userStopRequest) userStopResult {
	plan := oc.resolveUserStopPlan(req)
	return oc.executeUserStopPlan(ctx, req, plan)
}
