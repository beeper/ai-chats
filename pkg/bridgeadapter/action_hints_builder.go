package bridgeadapter

import (
	"encoding/json"

	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// ApprovalHintsParams holds the parameters for building standard Allow/Always/Deny action hints.
type ApprovalHintsParams struct {
	ApprovalID  string
	ToolCallID  string
	ToolName    string
	OwnerMXID   id.UserID
	ExpiresAtMs int64
}

// BuildApprovalHints constructs the standard Allow / Always Allow / Deny action hints
// used for tool approval requests.
func BuildApprovalHints(params ApprovalHintsParams) *event.BeeperActionHints {
	contextData, _ := json.Marshal(map[string]any{
		"approval_id":  params.ApprovalID,
		"tool_name":    params.ToolName,
		"tool_call_id": params.ToolCallID,
	})

	hints := &event.BeeperActionHints{
		Hints: []event.BeeperActionHint{
			{
				Body:      "Allow",
				EventType: "com.beeper.action_response",
				Event:     json.RawMessage(`{"action_id":"allow"}`),
			},
			{
				Body:      "Always Allow",
				EventType: "com.beeper.action_response",
				Event:     json.RawMessage(`{"action_id":"always"}`),
			},
			{
				Body:      "Deny",
				EventType: "com.beeper.action_response",
				Event:     json.RawMessage(`{"action_id":"deny"}`),
			},
		},
		Exclusive: true,
		Context:   contextData,
	}
	if params.OwnerMXID != "" {
		hints.AllowedSenders = []id.UserID{params.OwnerMXID}
	}
	if params.ExpiresAtMs > 0 {
		hints.ExpiresAt = params.ExpiresAtMs
	}
	return hints
}
