package openclaw

import (
	"testing"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

func TestShouldMirrorLatestUserMessageFromHistory(t *testing.T) {
	now := time.Date(2026, time.March, 11, 13, 22, 59, 0, time.UTC)

	t.Run("rejects beeper originated matrix events", func(t *testing.T) {
		payload := gatewayChatEvent{
			RunID: "run-web-1",
			TS:    now.UnixMilli(),
			Message: map[string]any{
				"role":      "assistant",
				"timestamp": now.UnixMilli(),
			},
		}
		message := map[string]any{
			"role":           "user",
			"timestamp":      now.Add(-2 * time.Second).UnixMilli(),
			"idempotencyKey": "$eventid:beeper.local",
		}
		if shouldMirrorLatestUserMessageFromHistory(payload, message) {
			t.Fatal("expected Matrix-originated user message to be skipped")
		}
	})

	t.Run("accepts matching webchat run id", func(t *testing.T) {
		payload := gatewayChatEvent{
			RunID: "run-web-2",
			TS:    now.UnixMilli(),
			Message: map[string]any{
				"role":      "assistant",
				"timestamp": now.UnixMilli(),
			},
		}
		message := map[string]any{
			"role":           "user",
			"timestamp":      now.Add(-3 * time.Second).UnixMilli(),
			"idempotencyKey": "run-web-2",
		}
		if !shouldMirrorLatestUserMessageFromHistory(payload, message) {
			t.Fatal("expected matching webchat user message to be mirrored")
		}
	})

	t.Run("rejects mismatched run markers", func(t *testing.T) {
		payload := gatewayChatEvent{
			RunID: "run-web-3",
			TS:    now.UnixMilli(),
			Message: map[string]any{
				"role":      "assistant",
				"timestamp": now.UnixMilli(),
			},
		}
		message := map[string]any{
			"role":           "user",
			"timestamp":      now.Add(-3 * time.Second).UnixMilli(),
			"idempotencyKey": "different-run",
		}
		if shouldMirrorLatestUserMessageFromHistory(payload, message) {
			t.Fatal("expected mismatched run markers to be skipped")
		}
	})

	t.Run("falls back to recent markerless messages only", func(t *testing.T) {
		payload := gatewayChatEvent{
			RunID: "run-web-4",
			TS:    now.UnixMilli(),
			Message: map[string]any{
				"role":      "assistant",
				"timestamp": now.UnixMilli(),
			},
		}
		recent := map[string]any{
			"role":      "user",
			"timestamp": now.Add(-2 * time.Minute).UnixMilli(),
		}
		if !shouldMirrorLatestUserMessageFromHistory(payload, recent) {
			t.Fatal("expected recent markerless user message to be mirrored as fallback")
		}

		stale := map[string]any{
			"role":      "user",
			"timestamp": now.Add(-(openClawHistoryMirrorFallbackWindow + time.Minute)).UnixMilli(),
		}
		if shouldMirrorLatestUserMessageFromHistory(payload, stale) {
			t.Fatal("expected stale markerless user message to be skipped")
		}
	})
}

func TestOpenClawRemoteMessageGetStreamOrderUsesGatewaySeq(t *testing.T) {
	ts := time.Date(2026, time.March, 12, 12, 0, 0, 0, time.UTC)
	first := buildOpenClawRemoteMessage(networkid.PortalKey{}, "first", bridgev2.EventSender{}, ts, 10, nil)
	second := buildOpenClawRemoteMessage(networkid.PortalKey{}, "second", bridgev2.EventSender{}, ts, 11, nil)
	if first.GetStreamOrder() != 10 {
		t.Fatalf("expected first stream order 10, got %d", first.GetStreamOrder())
	}
	if second.GetStreamOrder() != 11 {
		t.Fatalf("expected second stream order 11, got %d", second.GetStreamOrder())
	}
	if second.GetStreamOrder() <= first.GetStreamOrder() {
		t.Fatalf("expected gateway seq ordering to be strictly increasing")
	}
}
