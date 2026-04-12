package sdk

import (
	"testing"

	"maunium.net/go/mautrix/appservice"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

func TestMatrixMessageStatusEventInfoFallsBackToPortalRoom(t *testing.T) {
	portal := &bridgev2.Portal{
		Portal: &database.Portal{
			MXID: id.RoomID("!portal:test"),
		},
	}
	evt := &event.Event{
		ID:     id.EventID("$event:test"),
		Type:   event.EventMessage,
		Sender: id.UserID("@alice:test"),
		Content: event.Content{
			Parsed: &event.MessageEventContent{MsgType: event.MsgText},
			Raw: map[string]any{
				appservice.DoublePuppetKey: true,
			},
		},
	}

	info := MatrixMessageStatusEventInfo(portal, evt)
	if info == nil {
		t.Fatal("expected status event info")
	}
	if info.RoomID != portal.MXID {
		t.Fatalf("expected room id %q, got %q", portal.MXID, info.RoomID)
	}
	if info.SourceEventID != evt.ID {
		t.Fatalf("expected source event id %q, got %q", evt.ID, info.SourceEventID)
	}
	if !info.IsSourceEventDoublePuppeted {
		t.Fatal("expected double puppet flag to be preserved")
	}
}
