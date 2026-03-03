package bridgeadapter

import (
	"encoding/json"

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
func BuildApprovalHints(params ApprovalHintsParams) map[string]any {
	contextData, _ := json.Marshal(map[string]any{
		"approval_id":  params.ApprovalID,
		"tool_name":    params.ToolName,
		"tool_call_id": params.ToolCallID,
	})

	hints := map[string]any{
		"hints": []map[string]any{
			{"body": "Allow", "event_type": "m.room.message", "event": json.RawMessage(`{"action_id":"allow"}`)},
			{"body": "Always Allow", "event_type": "m.room.message", "event": json.RawMessage(`{"action_id":"always"}`)},
			{"body": "Deny", "event_type": "m.room.message", "event": json.RawMessage(`{"action_id":"deny"}`)},
		},
		"exclusive": true,
		"context":   contextData,
	}
	if params.OwnerMXID != "" {
		hints["allowed_senders"] = []id.UserID{params.OwnerMXID}
	}
	if params.ExpiresAtMs > 0 {
		hints["expires_at"] = params.ExpiresAtMs
	}
	return hints
}
