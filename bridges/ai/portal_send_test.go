package ai

import (
	"context"
	"strings"
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

func TestSendViaPortalRejectsMissingBridgeState(t *testing.T) {
	_, _, err := (&AIClient{}).sendViaPortal(context.Background(), &bridgev2.Portal{}, &bridgev2.ConvertedMessage{}, "")
	if err == nil {
		t.Fatal("expected bridge unavailable error")
	}
	if !strings.Contains(err.Error(), "bridge unavailable") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSendViaPortalRejectsInvalidPortal(t *testing.T) {
	oc := &AIClient{UserLogin: &bridgev2.UserLogin{Bridge: &bridgev2.Bridge{}}}

	_, _, err := oc.sendViaPortal(context.Background(), nil, &bridgev2.ConvertedMessage{}, "")
	if err == nil {
		t.Fatal("expected invalid portal error")
	}
	if !strings.Contains(err.Error(), "invalid portal") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSendEditViaPortalRejectsMissingBridgeState(t *testing.T) {
	err := (&AIClient{}).sendEditViaPortal(context.Background(), &bridgev2.Portal{}, networkid.MessageID("msg-1"), &bridgev2.ConvertedEdit{})
	if err == nil {
		t.Fatal("expected bridge unavailable error")
	}
	if !strings.Contains(err.Error(), "bridge unavailable") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSendEditViaPortalRejectsInvalidTargetMessage(t *testing.T) {
	oc := &AIClient{UserLogin: &bridgev2.UserLogin{Bridge: &bridgev2.Bridge{}}}
	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: "!room:example.com"}}

	err := oc.sendEditViaPortal(context.Background(), portal, "", &bridgev2.ConvertedEdit{})
	if err == nil {
		t.Fatal("expected invalid target message error")
	}
	if !strings.Contains(err.Error(), "invalid target message") {
		t.Fatalf("unexpected error: %v", err)
	}
}
