package streamtransport

import (
	"strings"

	"maunium.net/go/mautrix/event"
)

func BuildReplaceEditRaw(targetEventID string, body, formattedBody string, formatType event.Format) map[string]any {
	targetEventID = strings.TrimSpace(targetEventID)
	if targetEventID == "" {
		return nil
	}
	return map[string]any{
		"msgtype": event.MsgText,
		"body":    "* " + body,
		"m.new_content": map[string]any{
			"msgtype":        event.MsgText,
			"body":           body,
			"format":         formatType,
			"formatted_body": formattedBody,
			"m.mentions":     map[string]any{},
		},
		"m.relates_to": map[string]any{
			"rel_type": "m.replace",
			"event_id": targetEventID,
		},
		"com.beeper.dont_render_edited": true,
		"m.mentions":                    map[string]any{},
	}
}
