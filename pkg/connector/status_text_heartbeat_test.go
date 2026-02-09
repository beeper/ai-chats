package connector

import (
	"strings"
	"testing"
)

func TestFormatHeartbeatSummary_None(t *testing.T) {
	out := formatHeartbeatSummary(1000, nil)
	if out != "Heartbeat: none" {
		t.Fatalf("expected %q, got %q", "Heartbeat: none", out)
	}
}

func TestFormatHeartbeatSummary_Basic(t *testing.T) {
	now := int64(1_700_000_000_000)
	evt := &HeartbeatEventPayload{
		TS:      now - 60_000,
		Status:  "sent",
		Channel: "matrix",
		To:      "!room:example",
		Reason:  "interval",
		Preview: "line1\nline2",
	}
	out := formatHeartbeatSummary(now, evt)
	for _, want := range []string{"Heartbeat: sent", "channel=matrix", "to=!room:example", "reason=interval"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected output to contain %q, got %q", want, out)
		}
	}
	if !strings.Contains(out, "preview=") {
		t.Fatalf("expected output to include preview, got %q", out)
	}
}
