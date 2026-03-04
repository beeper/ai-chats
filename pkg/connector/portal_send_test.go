package connector

import (
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
)

func TestEnsureConvertedMessageParts_InitializesNilContent(t *testing.T) {
	converted := &bridgev2.ConvertedMessage{
		Parts: []*bridgev2.ConvertedMessagePart{
			{
				ID:   networkid.PartID("0"),
				Type: event.EventMessage,
				Extra: map[string]any{
					"body":    "Calling web_search...",
					"msgtype": "m.notice",
				},
			},
		},
	}

	ensureConvertedMessageParts(converted)

	if len(converted.Parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(converted.Parts))
	}
	if converted.Parts[0].Content == nil {
		t.Fatalf("expected content to be initialized")
	}
}

func TestEnsureConvertedMessageParts_DropsNilPart(t *testing.T) {
	converted := &bridgev2.ConvertedMessage{
		Parts: []*bridgev2.ConvertedMessagePart{
			nil,
			{
				ID:      networkid.PartID("1"),
				Type:    event.EventMessage,
				Content: &event.MessageEventContent{MsgType: event.MsgNotice, Body: "ok"},
			},
		},
	}

	ensureConvertedMessageParts(converted)

	if len(converted.Parts) != 1 {
		t.Fatalf("expected 1 part after sanitization, got %d", len(converted.Parts))
	}
	if converted.Parts[0] == nil {
		t.Fatalf("expected non-nil part")
	}
}
