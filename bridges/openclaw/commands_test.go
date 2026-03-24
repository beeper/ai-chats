package openclaw

import (
	"context"
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

func TestBuildOutboundPayloadPreservesSlashCommands(t *testing.T) {
	mgr := newOpenClawManager(&OpenClawClient{})

	msg := &bridgev2.MatrixMessage{
		MatrixEventBase: bridgev2.MatrixEventBase[*event.MessageEventContent]{
			Event:   &event.Event{Type: event.EventMessage},
			Content: &event.MessageEventContent{MsgType: event.MsgText, Body: "/model openai/gpt-5"},
		},
	}
	attachments, text, err := mgr.buildOutboundPayload(context.Background(), msg)
	if err != nil {
		t.Fatalf("buildOutboundPayload returned error: %v", err)
	}
	if len(attachments) != 0 {
		t.Fatalf("expected no attachments, got %#v", attachments)
	}
	if text != "/model openai/gpt-5" {
		t.Fatalf("expected slash command to pass through unchanged, got %q", text)
	}
}

func TestBuildOutboundPayloadPreservesStopCommand(t *testing.T) {
	mgr := newOpenClawManager(&OpenClawClient{})

	msg := &bridgev2.MatrixMessage{
		MatrixEventBase: bridgev2.MatrixEventBase[*event.MessageEventContent]{
			Event:   &event.Event{Type: event.EventMessage},
			Content: &event.MessageEventContent{MsgType: event.MsgText, Body: "/stop"},
		},
	}
	_, text, err := mgr.buildOutboundPayload(context.Background(), msg)
	if err != nil {
		t.Fatalf("buildOutboundPayload returned error: %v", err)
	}
	if text != "/stop" {
		t.Fatalf("expected stop command to pass through unchanged, got %q", text)
	}
}

func TestOpenClawPreferredGatewayMethodsDoNotRequireSessionPatch(t *testing.T) {
	for _, method := range openClawPreferredGatewayMethods {
		if method == "sessions.patch" {
			t.Fatal("did not expect sessions.patch in preferred gateway methods")
		}
	}
}
