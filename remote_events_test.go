package agentremote

import (
	"testing"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

func TestBuildReactionEventUsesExplicitStreamOrder(t *testing.T) {
	evt := BuildReactionEvent(
		networkid.PortalKey{},
		bridgev2.EventSender{},
		"target",
		"ok",
		"ok",
		time.UnixMilli(1_000),
		42,
		"test_target",
		nil,
		nil,
	)
	if got := evt.GetStreamOrder(); got != 42 {
		t.Fatalf("expected explicit stream order 42, got %d", got)
	}
}

func TestRemoteEditGetStreamOrderUsesExplicitValue(t *testing.T) {
	edit := &RemoteEdit{
		Timestamp:   time.UnixMilli(1_000),
		StreamOrder: 84,
	}
	if got := edit.GetStreamOrder(); got != 84 {
		t.Fatalf("expected explicit stream order 84, got %d", got)
	}
}
