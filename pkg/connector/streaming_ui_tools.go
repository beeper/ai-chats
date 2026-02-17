package connector

import (
	"context"
	"fmt"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

func (oc *AIClient) ensureUIToolInputStart(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	toolCallID string,
	toolName string,
	providerExecuted bool,
	dynamic bool,
	title string,
	providerMetadata map[string]any,
) {
	if toolCallID == "" {
		return
	}
	if !state.uiToolStarted[toolCallID] {
		state.uiToolStarted[toolCallID] = true
		if strings.TrimSpace(toolName) != "" {
			state.uiToolNameByToolCallID[toolCallID] = toolName
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
		oc.emitStreamEvent(ctx, portal, state, part)
	}
	if strings.TrimSpace(toolName) != "" {
		state.uiToolNameByToolCallID[toolCallID] = toolName
	}
}

func (oc *AIClient) emitUIToolInputDelta(ctx context.Context, portal *bridgev2.Portal, state *streamingState, toolCallID, toolName, delta string, providerExecuted bool) {
	if toolCallID == "" {
		return
	}
	oc.ensureUIToolInputStart(ctx, portal, state, toolCallID, toolName, providerExecuted, false, toolDisplayTitle(toolName), nil)
	if delta != "" {
		oc.emitStreamEvent(ctx, portal, state, map[string]any{
			"type":           "tool-input-delta",
			"toolCallId":     toolCallID,
			"inputTextDelta": delta,
		})
	}
}

func (oc *AIClient) emitUIToolInputAvailable(ctx context.Context, portal *bridgev2.Portal, state *streamingState, toolCallID, toolName string, input any, providerExecuted bool) {
	if toolCallID == "" {
		return
	}
	oc.ensureUIToolInputStart(ctx, portal, state, toolCallID, toolName, providerExecuted, false, toolDisplayTitle(toolName), nil)
	oc.emitStreamEvent(ctx, portal, state, map[string]any{
		"type":             "tool-input-available",
		"toolCallId":       toolCallID,
		"toolName":         toolName,
		"input":            input,
		"providerExecuted": providerExecuted,
	})
}

func (oc *AIClient) emitUIToolInputError(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	toolCallID, toolName string,
	input any,
	errorText string,
	providerExecuted bool,
	dynamic bool,
) {
	if toolCallID == "" {
		return
	}
	oc.ensureUIToolInputStart(ctx, portal, state, toolCallID, toolName, providerExecuted, dynamic, toolDisplayTitle(toolName), nil)
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
	oc.emitStreamEvent(ctx, portal, state, part)
}

func (oc *AIClient) emitUIToolApprovalRequest(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	approvalID string,
	toolCallID string,
	toolName string,
	targetEventID id.EventID,
	ttlSeconds int,
) {
	if strings.TrimSpace(approvalID) == "" || strings.TrimSpace(toolCallID) == "" {
		return
	}
	if state == nil {
		// Without a streaming state we can't track approvals or emit stream events safely.
		return
	}
	state.uiToolCallIDByApproval[approvalID] = toolCallID
	oc.emitStreamEvent(ctx, portal, state, map[string]any{
		"type":       "tool-approval-request",
		"approvalId": approvalID,
		"toolCallId": toolCallID,
		"toolName":   toolName,
		"ttlSeconds": ttlSeconds,
	})

	// Send a second tool_call timeline event with approval data so the desktop
	// ToolEventGrouper can render inline approval buttons.
	approvalExpiresAtMs := int64(0)
	if ttlSeconds > 0 {
		approvalExpiresAtMs = time.Now().Add(time.Duration(ttlSeconds) * time.Second).UnixMilli()
	}
	oc.sendToolCallApprovalEvent(ctx, portal, state, toolCallID, toolName, approvalID, approvalExpiresAtMs)

	// Back-compat fallback: many clients either don't support or don't render our
	// ephemeral stream events. If approvals are required, give the user a clear,
	// timeline-visible way to proceed (!ai approve or UI card).
	if state.suppressSend {
		return
	}
	if portal == nil || portal.MXID == "" {
		return
	}
	// Avoid spamming the timeline for the same approval. The ephemeral event is
	// still emitted above (so capable clients can render the native UI).
	if state.timelineNotices == nil {
		state.timelineNotices = make(map[string]bool)
	}
	noticeKey := "approval:" + approvalID
	if state.timelineNotices[noticeKey] {
		return
	}
	state.timelineNotices[noticeKey] = true

	if strings.TrimSpace(toolName) == "" {
		toolName = "tool"
	}
	mins := 0
	if ttlSeconds > 0 {
		mins = (ttlSeconds + 59) / 60
	}
	expires := ""
	if mins > 0 {
		expires = fmt.Sprintf(" Expires in %d min.", mins)
	}
	body := fmt.Sprintf(
		"Approval required to run %s. Type !ai approve %s allow|always|deny.%s",
		toolName,
		approvalID,
		expires,
	)
	expiresAtMs := int64(0)
	if ttlSeconds > 0 {
		expiresAtMs = time.Now().Add(time.Duration(ttlSeconds) * time.Second).UnixMilli()
	}

	uiMessage := map[string]any{
		"id":   "approval:" + approvalID,
		"role": "assistant",
		"metadata": map[string]any{
			"turn_id":      state.turnID,
			"approval_id":  approvalID,
			"tool_call_id": toolCallID,
			// Allows clients to disable the UI locally even if the snapshot isn't edited on timeout.
			"approval_expires_at_ms": expiresAtMs,
		},
		"parts": []map[string]any{
			{
				"type":       "dynamic-tool",
				"toolName":   toolName,
				"toolCallId": toolCallID,
				"state":      "approval-requested",
				"approval": map[string]any{
					"id":          approvalID,
					"expiresAtMs": expiresAtMs,
				},
			},
		},
	}

	raw := map[string]any{
		"body":       body,
		"msgtype":    event.MsgNotice,
		BeeperAIKey:  uiMessage,
		"m.mentions": map[string]any{},
	}
	if targetEventID != "" {
		raw["m.relates_to"] = map[string]any{
			"rel_type": RelReference,
			"event_id": targetEventID.String(),
		}
	}
	content := &event.Content{Raw: raw}

	// Prefer sending as the model/assistant identity if possible (so the message
	// reads as part of the assistant's flow), but fall back to the bridge bot.
	if intent := oc.getModelIntent(ctx, portal); intent != nil {
		if resp, err := intent.SendMessage(ctx, portal.MXID, event.EventMessage, content, nil); err == nil {
			if resp != nil && resp.EventID != "" {
				oc.setApprovalSnapshotEvent(approvalID, resp.EventID, false)
			}
			return
		}
	}
	if oc != nil && oc.UserLogin != nil && oc.UserLogin.Bridge != nil && oc.UserLogin.Bridge.Bot != nil {
		if resp, err := oc.UserLogin.Bridge.Bot.SendMessage(ctx, portal.MXID, event.EventMessage, content, nil); err == nil {
			if resp != nil && resp.EventID != "" {
				oc.setApprovalSnapshotEvent(approvalID, resp.EventID, true)
			}
		}
	}
}

func (oc *AIClient) emitUIToolOutputAvailable(ctx context.Context, portal *bridgev2.Portal, state *streamingState, toolCallID string, output any, providerExecuted bool, preliminary bool) {
	if toolCallID == "" {
		return
	}
	if state != nil && !preliminary {
		if state.uiToolOutputFinalized[toolCallID] {
			return
		}
		state.uiToolOutputFinalized[toolCallID] = true
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
	oc.emitStreamEvent(ctx, portal, state, part)
}

func (oc *AIClient) emitUIToolOutputDenied(ctx context.Context, portal *bridgev2.Portal, state *streamingState, toolCallID string) {
	if strings.TrimSpace(toolCallID) == "" {
		return
	}
	if state != nil {
		if state.uiToolOutputFinalized[toolCallID] {
			return
		}
		state.uiToolOutputFinalized[toolCallID] = true
	}
	oc.emitStreamEvent(ctx, portal, state, map[string]any{
		"type":       "tool-output-denied",
		"toolCallId": toolCallID,
	})
}

func (oc *AIClient) emitUIToolOutputError(ctx context.Context, portal *bridgev2.Portal, state *streamingState, toolCallID, errorText string, providerExecuted bool) {
	if toolCallID == "" {
		return
	}
	if state != nil {
		if state.uiToolOutputFinalized[toolCallID] {
			return
		}
		state.uiToolOutputFinalized[toolCallID] = true
	}
	oc.emitStreamEvent(ctx, portal, state, map[string]any{
		"type":             "tool-output-error",
		"toolCallId":       toolCallID,
		"errorText":        errorText,
		"providerExecuted": providerExecuted,
	})
}
