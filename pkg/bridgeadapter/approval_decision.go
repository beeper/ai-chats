package bridgeadapter

import (
	"encoding/json"
	"strings"

	"maunium.net/go/mautrix/event"
)

// ActionResponsePayload holds the parsed content of a com.beeper.action_response event.
type ActionResponsePayload struct {
	ActionID   string
	ApprovalID string
	ToolCallID string
	EventID    string // event_id of the message containing action hints
}

// ParseActionResponse extracts an ActionResponsePayload from a BeeperActionResponseEventContent,
// returning nil when the payload is incomplete.
func ParseActionResponse(content *event.BeeperActionResponseEventContent) *ActionResponsePayload {
	if content == nil {
		return nil
	}
	actionID := strings.TrimSpace(content.ActionID)
	if actionID == "" {
		return nil
	}

	payload := &ActionResponsePayload{
		ActionID: actionID,
	}

	// Parse context for approval_id and tool_call_id
	if len(content.Context) > 0 {
		var ctx map[string]any
		if err := json.Unmarshal(content.Context, &ctx); err == nil {
			if aid, ok := ctx["approval_id"].(string); ok {
				payload.ApprovalID = strings.TrimSpace(aid)
			}
			if tcid, ok := ctx["tool_call_id"].(string); ok {
				payload.ToolCallID = strings.TrimSpace(tcid)
			}
		}
	}

	// Parse m.from_action_hint from RelatesTo raw content.
	// This is a custom relation key not represented in mautrix-go's RelatesTo struct,
	// so we parse it from the event's raw content.
	if content.RelatesTo != nil {
		// The event_id in RelatesTo.EventID may contain the target event ID if set,
		// but m.from_action_hint is an additional relation field.
		payload.EventID = strings.TrimSpace(content.RelatesTo.EventID.String())
	}

	return payload
}

// ActionDecisionFromString converts an action_id string from a com.beeper.action_response
// into structured booleans (approve, always, ok).
func ActionDecisionFromString(actionID string) (approve bool, always bool, ok bool) {
	switch strings.ToLower(strings.TrimSpace(actionID)) {
	case "allow", "approve", "yes", "y", "true", "1", "once":
		return true, false, true
	case "always", "always-allow", "allow-always":
		return true, true, true
	case "deny", "no", "n", "false", "0", "reject":
		return false, false, true
	default:
		return false, false, false
	}
}
